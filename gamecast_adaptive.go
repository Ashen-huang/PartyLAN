package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	appevents "PartyLAN/internal/app"
	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/gamecast/adaptive"
	"PartyLAN/internal/gamecast/nativehost"
	"PartyLAN/internal/gamecast/screegop2p"
	"PartyLAN/internal/video"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const gameCastQualityPollInterval = 2 * time.Second

type gameCastQualityUpdate struct {
	snapshot gamecast.QualitySnapshot
	fetched  time.Time
}

func (a *App) runGameCastQualityPoller(ctx context.Context, roomID, participantID string, updates chan gameCastQualityUpdate) {
	ticker := time.NewTicker(gameCastQualityPollInterval)
	defer ticker.Stop()
	var lastFailureLog time.Time
	for {
		client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
		if err == nil {
			requestCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			snapshot, requestErr := client.GetQualitySnapshot(requestCtx, roomID, participantID)
			cancel()
			if requestErr == nil {
				offerLatestQualityUpdate(updates, gameCastQualityUpdate{snapshot: snapshot, fetched: time.Now()})
			} else if time.Since(lastFailureLog) >= 10*time.Second {
				log.Printf("[gamecast/quality] snapshot unavailable room=%s: %v", roomID, requestErr)
				lastFailureLog = time.Now()
			}
		} else if time.Since(lastFailureLog) >= 10*time.Second {
			log.Printf("[gamecast/quality] client unavailable room=%s: %v", roomID, err)
			lastFailureLog = time.Now()
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func offerLatestQualityUpdate(updates chan gameCastQualityUpdate, update gameCastQualityUpdate) {
	select {
	case updates <- update:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- update:
	default:
	}
}

func qualityViewerSamples(update gameCastQualityUpdate) []adaptive.ViewerSample {
	if update.fetched.IsZero() {
		return nil
	}
	out := make([]adaptive.ViewerSample, 0, len(update.snapshot.Samples))
	for _, sample := range update.snapshot.Samples {
		out = append(out, adaptive.ViewerSample{
			At:                   update.fetched,
			ViewerID:             sample.ParticipantID,
			SampleID:             fmt.Sprintf("%.6f", sample.ReportedAtUnix),
			Generation:           sample.Generation,
			PairID:               sample.PairID,
			Valid:                sample.AgeSeconds <= 8,
			PacketsReceivedDelta: sample.PacketsReceivedDelta,
			PacketsLostDelta:     sample.PacketsLostDelta,
			FramesDroppedDelta:   sample.FramesDroppedDelta,
			DecodedFPS:           sample.DecodedFPS,
			IncomingBitrateKbps:  sample.IncomingBitrateKbps,
			JitterMS:             sample.JitterMS,
		})
	}
	return out
}

func adaptiveProfileFromLayer(layer nativehost.VideoLayer) adaptive.Profile {
	return adaptive.Profile{
		Width:       layer.Width,
		Height:      layer.Height,
		FPS:         layer.FPS,
		BitrateKbps: layer.BitrateKbps,
	}
}

func videoLayerFromAdaptive(profile adaptive.Profile) nativehost.VideoLayer {
	return nativehost.VideoLayer{
		RID:         "f",
		Width:       profile.Width,
		Height:      profile.Height,
		FPS:         profile.FPS,
		BitrateKbps: profile.BitrateKbps,
	}
}

func effectiveAdaptiveProfile(profile adaptive.Profile, safeEgressKbps, viewerCount int) adaptive.Profile {
	if safeEgressKbps <= 0 || viewerCount <= 0 {
		return profile
	}
	perViewer := safeEgressKbps / viewerCount
	if perViewer > 0 && perViewer < profile.BitrateKbps {
		profile.BitrateKbps = perViewer
	}
	return profile
}

type gameCastEncoderPreparation struct {
	entry      gameCastVideoEncoder
	profile    adaptive.Profile
	decision   *adaptive.Decision
	encodedIDR []byte
	encodeTime time.Duration
	err        error
}

func prepareGameCastEncoderAsync(
	ctx context.Context,
	profile adaptive.Profile,
	decision *adaptive.Decision,
	frame *video.Frame,
) <-chan gameCastEncoderPreparation {
	result := make(chan gameCastEncoderPreparation)
	cloned := cloneVideoFrame(frame)
	go func() {
		prepared := gameCastEncoderPreparation{profile: profile, decision: decision}
		entry, err := newGameCastVideoEncoder(videoLayerFromAdaptive(profile))
		if err != nil {
			prepared.err = err
		} else {
			prepared.entry = entry
			if err := entry.encoder.ForceKeyFrame(); err != nil {
				prepared.err = fmt.Errorf("force prepared keyframe: %w", err)
			} else {
				started := time.Now()
				prepared.encodedIDR, prepared.err = entry.encoder.Encode(
					prepareWebRTCFrameForEncode(cloned, profile.Width, profile.Height),
				)
				prepared.encodeTime = time.Since(started)
				if prepared.err == nil && !h264PreparedAccessUnit(prepared.encodedIDR) {
					prepared.err = fmt.Errorf("prepared access unit omitted SPS/PPS or IDR")
				}
			}
		}
		if prepared.err != nil && prepared.entry.encoder != nil {
			_ = prepared.entry.encoder.Close()
			prepared.entry.encoder = nil
		}
		select {
		case result <- prepared:
		case <-ctx.Done():
			if prepared.entry.encoder != nil {
				_ = prepared.entry.encoder.Close()
			}
		}
	}()
	return result
}

func h264PreparedAccessUnit(data []byte) bool {
	hasSPS := false
	hasPPS := false
	hasIDR := false
	for offset := 0; offset+4 < len(data); {
		start, prefix := findAnnexBStartCode(data, offset)
		if start < 0 || start+prefix >= len(data) {
			break
		}
		nalType := data[start+prefix] & 0x1f
		switch nalType {
		case 5:
			hasIDR = true
		case 7:
			hasSPS = true
		case 8:
			hasPPS = true
		}
		offset = start + prefix + 1
	}
	return hasSPS && hasPPS && hasIDR
}

func findAnnexBStartCode(data []byte, from int) (int, int) {
	for index := maxInt(from, 0); index+3 < len(data); index++ {
		if data[index] != 0 || data[index+1] != 0 {
			continue
		}
		if data[index+2] == 1 {
			return index, 3
		}
		if index+3 < len(data) && data[index+2] == 0 && data[index+3] == 1 {
			return index, 4
		}
	}
	return -1, 0
}

func newGameCastVideoEncoder(layer nativehost.VideoLayer) (gameCastVideoEncoder, error) {
	encoders, err := newGameCastVideoEncoders([]nativehost.VideoLayer{layer})
	if err != nil {
		return gameCastVideoEncoder{}, err
	}
	return encoders[0], nil
}

func cloneVideoFrame(frame *video.Frame) *video.Frame {
	if frame == nil {
		return nil
	}
	return &video.Frame{
		Data:      append([]byte(nil), frame.Data...),
		Timestamp: frame.Timestamp,
		Width:     frame.Width,
		Height:    frame.Height,
	}
}

func profilesEqual(left, right adaptive.Profile) bool {
	return left == right
}

func formatAdaptiveProfile(profile adaptive.Profile) string {
	return fmt.Sprintf("%dx%d@%d/%dkbps", profile.Width, profile.Height, profile.FPS, profile.BitrateKbps)
}

func (a *App) emitGameCastAdaptiveStats(
	local adaptive.LocalSample,
	status adaptive.Status,
	actual adaptive.Profile,
	sessions int,
	mode string,
) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(appevents.EventStreamStats), appevents.StreamStatsEvent{
		PeerID:             "gamecast-host",
		Width:              actual.Width,
		Height:             actual.Height,
		Mode:               mode,
		EncodedFPS:         local.EncodedFPS,
		EstimatedSourceFPS: local.FreshFPS,
		BitrateKbps:        local.BitrateKbps,
		Sessions:           sessions,
		RequestedWidth:     status.Requested.Width,
		RequestedHeight:    status.Requested.Height,
		RequestedFPS:       status.Requested.FPS,
		ActualFPS:          actual.FPS,
		FreshFPS:           local.FreshFPS,
		RepeatedRatio:      local.RepeatedRatio,
		EncodeBudgetRatio:  local.EncodeBudgetRatio,
		AdaptiveState:      status.State,
		AdaptiveReason:     status.Reason,
		AdaptivePolicy:     status.PolicyVersion,
		AdaptiveLevel:      status.Level,
		AdaptiveLevels:     status.Levels,
		WeakViewers:        status.WeakViewers,
	})
}

func (a *App) emitGameCastP2PLayerStats(
	pool *gameCastP2PLayerPool,
	controller *adaptive.PerViewerController,
	snapshots []screegop2p.ViewerTransportSnapshot,
	overloaded bool,
) {
	if a.ctx == nil || pool == nil || controller == nil {
		return
	}
	levels := pool.ReadyLevels()
	if len(levels) == 0 {
		return
	}
	status := controller.Status(time.Now())
	weak := 0
	constrained := 0
	for _, entry := range status {
		if entry.Weak {
			weak++
		}
	}
	for _, snapshot := range snapshots {
		entry, ok := status[snapshot.PeerID]
		if !ok {
			continue
		}
		assigned, err := strconv.Atoi(snapshot.LayerID)
		if err == nil && assigned < entry.Level {
			constrained++
		}
	}
	summaries := make([]string, 0, len(levels))
	totalBitrate := 0
	for _, level := range levels {
		if level < 0 || level >= len(pool.ladder) {
			continue
		}
		profile := pool.ladder[level]
		totalBitrate += profile.BitrateKbps
		summaries = append(summaries, fmt.Sprintf("%d:%dx%d@%d/%d", level, profile.Width, profile.Height, profile.FPS, profile.BitrateKbps))
	}
	primary := pool.ladder[levels[0]]
	poolMode := "single-layer-compatible"
	if len(levels) >= gameCastP2PMaxLayers {
		poolMode = "full-pool"
	} else if len(levels) > 1 {
		poolMode = "reduced-pool"
	}
	reason := ""
	if overloaded {
		reason = "encoder-overload"
	}
	runtime.EventsEmit(a.ctx, string(appevents.EventStreamStats), appevents.StreamStatsEvent{
		PeerID: "gamecast-host", Width: primary.Width, Height: primary.Height,
		Mode: "p2p-layer-pool", BitrateKbps: float64(totalBitrate), Sessions: len(snapshots),
		RequestedWidth: primary.Width, RequestedHeight: primary.Height, RequestedFPS: primary.FPS,
		ActualFPS: primary.FPS, AdaptiveState: poolMode, AdaptiveReason: reason,
		AdaptivePolicy: adaptive.PerViewerPolicyVersion, AdaptiveLevels: len(pool.ladder),
		WeakViewers: weak, ActiveLayers: len(levels), ConstrainedViewers: constrained,
		LayerSummary: strings.Join(summaries, ","),
	})
}
