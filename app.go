package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"PartyLAN/internal/app"
	"PartyLAN/internal/appmode"
	"PartyLAN/internal/audio"
	"PartyLAN/internal/media"
	"PartyLAN/internal/native/windowsaudio"
	"PartyLAN/internal/network"
	"PartyLAN/internal/relaycfg"
	"PartyLAN/internal/room"
	"PartyLAN/internal/video"
	"PartyLAN/internal/voice"
	"PartyLAN/pkg/config"
	"PartyLAN/pkg/version"

	ps "github.com/mitchellh/go-ps"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultShareFPS = 30
	maxShareFPS     = 120
	previewUIFPS    = 15

	defaultNetworkStartTimeout = 5 * time.Second
	authKeyNetworkStartTimeout = 30 * time.Second

	defaultWindowWidth      = 1400
	defaultWindowHeight     = 810
	maxWindowWidthRatio     = 0.96
	maxWindowHeightRatio    = 0.92
	smallScreenWidthCutoff  = 1800
	smallScreenHeightCutoff = 1100
	windowLayoutVersion     = 4
	maxDisplayNameRunes     = 40
)

type App struct {
	ctx                 context.Context
	network             network.Network
	room                *room.RoomImpl
	hostVideoCapability video.BackendCapability
	audioCapability     windowsaudio.Capability
	cfg                 *config.Config
	relayConfig         relaycfg.RuntimeConfig
	audioShareSession   *audio.ShareSession
	voiceChannel        voice.VoiceChannel
	mediaManager        *media.Manager
	mediaCancel         context.CancelFunc
	mediaDone           chan struct{}
	audioMediaCancel    context.CancelFunc
	audioMediaDone      chan struct{}
	mediaKeyFrameSeq    atomic.Int64

	mu                  sync.Mutex
	senders             map[string]*room.StreamSender
	receivers           map[string]*room.StreamReceiver
	activeShare         *shareSession
	selfSharing         bool
	voiceActive         bool
	voiceMuted          bool
	voiceVolume         float32
	voicePeerID         string
	audioShareSelection map[string]bool
}

func NewApp(net network.Network) *App {
	return &App{
		network:             net,
		senders:             make(map[string]*room.StreamSender),
		receivers:           make(map[string]*room.StreamReceiver),
		audioShareSelection: make(map[string]bool),
		audioShareSession:   audio.NewShareSession(),
		voiceVolume:         0.8,
	}
}

type AudioShareCandidate struct {
	PID         int    `json:"pid"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IconDataURL string `json:"iconDataUrl"`
	Active      bool   `json:"active"`
	Selected    bool   `json:"selected"`
}

type UserPreferences struct {
	DisplayName string `json:"displayName"`
}

type audioCaptureTarget struct {
	PID  uint32
	Name string
}

type VoiceStateSnapshot struct {
	Active       bool   `json:"active"`
	Muted        bool   `json:"muted"`
	Volume       int    `json:"volume"`
	State        int    `json:"state"`
	PeerID       string `json:"peerId"`
	Experimental bool   `json:"experimental"`
	Message      string `json:"message"`
}

type shareSession struct {
	displayIndex int
	width        int
	height       int
	fps          int
}

func (s shareSession) captureConfig() video.CaptureConfig {
	return video.CaptureConfig{
		DisplayIndex: s.displayIndex,
		FrameRate:    s.fps,
	}
}

func networkStartTimeout(authKey string) time.Duration {
	if strings.TrimSpace(authKey) != "" {
		return authKeyNetworkStartTimeout
	}
	return defaultNetworkStartTimeout
}

func adaptiveWindowSize(screenWidth, screenHeight int) (int, int) {
	if screenWidth <= 0 || screenHeight <= 0 {
		return defaultWindowWidth, defaultWindowHeight
	}

	maxWidth := int(float64(screenWidth) * maxWindowWidthRatio)
	maxHeight := int(float64(screenHeight) * maxWindowHeightRatio)
	if maxWidth <= 0 || maxHeight <= 0 {
		return defaultWindowWidth, defaultWindowHeight
	}

	width := minInt(defaultWindowWidth, maxWidth)
	height := minInt(defaultWindowHeight, maxHeight)

	if width*9 > height*16 {
		width = (height * 16) / 9
	} else {
		height = (width * 9) / 16
	}

	if width > maxWidth {
		width = maxWidth
	}
	if height > maxHeight {
		height = maxHeight
	}
	if width <= 0 {
		width = maxWidth
	}
	if height <= 0 {
		height = maxHeight
	}

	return width, height
}

func (a *App) applyInitialWindowSizing() {
	if a.ctx == nil {
		return
	}

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg == nil || cfg.WindowLayoutVersion >= windowLayoutVersion {
		return
	}

	screens, err := runtime.ScreenGetAll(a.ctx)
	if err != nil {
		log.Printf("screen query failed: %v", err)
		return
	}
	if len(screens) == 0 {
		return
	}

	screen := screens[0]
	for _, candidate := range screens {
		if candidate.IsCurrent {
			screen = candidate
			break
		}
		if candidate.IsPrimary {
			screen = candidate
		}
	}

	log.Printf("[window/layout] first-run screen logical=%dx%d current=%t primary=%t layout_version=%d",
		screen.Size.Width, screen.Size.Height, screen.IsCurrent, screen.IsPrimary, windowLayoutVersion)

	width, height := adaptiveWindowSize(screen.Size.Width, screen.Size.Height)
	if screen.Size.Width <= smallScreenWidthCutoff || screen.Size.Height <= smallScreenHeightCutoff {
		log.Printf("[window/layout] applying first-run maximise for compact display")
		runtime.WindowMaximise(a.ctx)
	} else {
		if width <= 0 || height <= 0 {
			return
		}
		log.Printf("[window/layout] applying first-run size %dx%d", width, height)
		runtime.WindowSetSize(a.ctx, width, height)
		runtime.WindowCenter(a.ctx)
	}

	a.mu.Lock()
	if a.cfg != nil {
		a.cfg.WindowAutoSized = true
		a.cfg.WindowLayoutVersion = windowLayoutVersion
	}
	a.mu.Unlock()
	a.persistConfig()
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.hostVideoCapability = video.NewNativeRuntimeBackend().Capability()
	a.audioCapability = windowsaudio.NewBridge().QueryCapability()

	cfg, err := config.Load()
	if err != nil {
		log.Printf("load config failed: %v", err)
		cfg = config.DefaultConfig()
	}
	a.cfg = cfg
	a.relayConfig = relaycfg.Resolve(cfg)
	for _, line := range a.relayConfig.MediaHealthLines() {
		log.Printf("[media/ice] %s", line)
	}
	if setter, ok := a.network.(network.DERPExpectationSetter); ok {
		setter.SetDERPExpectation(network.DERPExpectation{
			RegionID:   a.relayConfig.DERPRegionID,
			RegionCode: a.relayConfig.DERPRegionCode,
			HostName:   a.relayConfig.DERPHostName,
			DERPPort:   a.relayConfig.DERPPort,
		})
	}
	if setter, ok := a.network.(network.TSNetControlURLSetter); ok {
		setter.SetTSNetControlURL(a.relayConfig.TSControlURL)
	}
	audioShareSelection := normalizeAudioShareSelection(cfg.AudioShareSources)
	cfg.AudioShareSources = audioShareSelection
	for _, source := range audioShareSelection {
		key := normalizeAudioProcessNameKey(source)
		if key != "" {
			a.audioShareSelection[key] = true
		}
	}
	a.audioShareSession.SetSelected(audioShareSelection)

	authKey := os.Getenv("PARTYLAN_AUTH_KEY")
	if authKey == "" && a.cfg != nil {
		authKey = a.cfg.TailscaleAuthKey
	}

	startCtx, cancel := context.WithTimeout(ctx, networkStartTimeout(authKey))
	defer cancel()

	if err := a.network.Start(startCtx, authKey); err != nil {
		log.Printf("network start failed: %v", err)
		a.emitVideoCapability()
		a.emitAudioCapability()
		a.emitRuntimeStatus()
		a.emitError(app.ErrCodeNetworkStart, "缃戠粶鍚姩澶辫触锛岃妫€鏌ョ綉缁滅幆澧冦€佽璇侀厤缃垨褰撳墠鏋勫缓妯″紡")
		return
	}

	a.network.OnNodeStateChange(func(node network.NodeInfo) {
		a.emitNetworkStatus()
	})

	a.emitNetworkStatus()
	a.emitVideoCapability()
	a.emitAudioCapability()
	a.emitRuntimeStatus()
	go a.networkStatusLoop(ctx)
}

func (a *App) domReady(ctx context.Context) {
	a.ctx = ctx
	a.applyInitialWindowSizing()
}

func (a *App) shutdown(ctx context.Context) {
	a.persistConfig()
	if err := a.network.Stop(); err != nil {
		log.Printf("network stop failed: %v", err)
		a.emitError(app.ErrCodeNetworkStop, "缃戠粶鍏抽棴澶辫触")
	}
}

func (a *App) CreateRoom(name, password string) error {
	if a.room != nil {
		return fmt.Errorf("宸插湪鎴块棿涓紝鏃犳硶閲嶅鍒涘缓")
	}

	r := room.NewRoomImpl(a.network)
	r.SetSelfHostname(a.configuredDisplayName())
	a.setupRoomCallbacks(r)

	cfg := room.RoomConfig{
		ID:       r.SelfID(),
		Name:     name,
		Password: password,
	}

	if err := r.Create(cfg); err != nil {
		log.Printf("create room failed: %v", err)
		return fmt.Errorf("create room: %w", err)
	}

	a.room = r
	runtime.EventsEmit(a.ctx, string(app.EventPeerJoined), app.PeerEvent{
		PeerID:   r.SelfID(),
		Hostname: r.SelfHostname(),
	})
	runtime.EventsEmit(a.ctx, string(app.EventRoomCreated), map[string]interface{}{
		"roomId":     cfg.ID,
		"name":       cfg.Name,
		"hostname":   r.SelfHostname(),
		"selfPeerId": r.SelfID(),
	})
	for _, peer := range r.GetPeers() {
		runtime.EventsEmit(a.ctx, string(app.EventPeerJoined), app.PeerEvent{
			PeerID:   peer.ID,
			Hostname: peer.Hostname,
		})
	}

	return nil
}

func (a *App) JoinRoom(targetNodeID, password string) error {
	if a.room != nil {
		return fmt.Errorf("宸插湪鎴块棿涓紝鏃犳硶閲嶅鍔犲叆")
	}
	targetNodeID = normalizeJoinTarget(targetNodeID)
	if targetNodeID == "" {
		return fmt.Errorf("璇疯緭鍏ョ洰鏍囦富鏈鸿妭鐐?ID")
	}

	log.Printf("[room/join-request] target=%s", targetNodeID)
	r := room.NewRoomImpl(a.network)
	r.SetSelfHostname(a.configuredDisplayName())
	a.setupRoomCallbacks(r)

	if err := r.Join(targetNodeID, password); err != nil {
		log.Printf("join room failed: %v", err)
		return fmt.Errorf("join room: %w", err)
	}

	if _, err := r.ConnectToPeer(targetNodeID); err != nil {
		_ = r.Leave()
		log.Printf("connect to target host failed: %v", err)
		return fmt.Errorf("connect to target host: %w", err)
	}

	a.room = r
	runtime.EventsEmit(a.ctx, string(app.EventPeerJoined), app.PeerEvent{
		PeerID:   r.SelfID(),
		Hostname: r.SelfHostname(),
	})
	runtime.EventsEmit(a.ctx, string(app.EventRoomJoined), map[string]interface{}{
		"roomId":     targetNodeID,
		"selfPeerId": r.SelfID(),
	})
	for _, peer := range r.GetPeers() {
		runtime.EventsEmit(a.ctx, string(app.EventPeerJoined), app.PeerEvent{
			PeerID:   peer.ID,
			Hostname: peer.Hostname,
		})
	}

	return nil
}

func normalizeJoinTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if target == "" {
		return ""
	}

	for _, prefix := range []string{"partylan://", "tsnet://", "tcp://"} {
		target = strings.TrimPrefix(target, prefix)
	}
	if idx := strings.IndexAny(target, "/?#"); idx >= 0 {
		target = target[:idx]
	}
	if host, _, err := net.SplitHostPort(target); err == nil && host != "" {
		target = host
	}

	return strings.Trim(strings.TrimSpace(target), "[]")
}

func (a *App) LeaveRoom() error {
	voiceCh, voicePeerID := a.detachVoiceChannel()
	if voiceCh != nil {
		_ = voiceCh.Stop()
	}
	if voicePeerID != "" {
		a.emitVoiceState(voicePeerID, int(voice.VoiceStateIdle))
	}

	a.stopMediaLoop()

	a.mu.Lock()
	if a.mediaManager != nil {
		a.mediaManager.StopShare()
		a.mediaManager = nil
	}
	for id, s := range a.senders {
		s.Stop()
		delete(a.senders, id)
	}
	for id, r := range a.receivers {
		r.Stop()
		delete(a.receivers, id)
	}
	a.activeShare = nil
	a.selfSharing = false
	a.mu.Unlock()

	if a.room == nil {
		return nil
	}

	if err := a.room.Leave(); err != nil {
		log.Printf("leave room failed: %v", err)
		return fmt.Errorf("leave room: %w", err)
	}
	a.room = nil

	runtime.EventsEmit(a.ctx, string(app.EventRoomLeft), nil)
	return nil
}

func (a *App) StartSharing(displayIndex int, targetWidth int, targetHeight int, fps int) error {
	if a.room == nil {
		return fmt.Errorf("create or join a room first")
	}

	if !a.hostVideoCapability.SupportsHost || a.hostVideoCapability.Backend != video.BackendNative {
		log.Printf("screen sharing unavailable: %s", a.hostVideoCapability.Reason)
		a.emitError(app.ErrCodeVideoCapability, "WebRTC sharing requires native H.264 host support; software-backend fallback is disabled")
		return fmt.Errorf("WebRTC sharing requires native H.264 host support; software-backend fallback is disabled")
	}
	if ok, reason := webRTCBrowserTransportAvailable(a.network); !ok {
		a.emitError(app.ErrCodeVideoCapability, reason)
		return fmt.Errorf("%s", reason)
	}

	a.mu.Lock()
	if a.selfSharing {
		a.mu.Unlock()
		return fmt.Errorf("褰撳墠宸茬粡鍦ㄥ叡浜腑")
	}
	a.mu.Unlock()

	displays := video.ListDisplays()
	if displayIndex < 0 || displayIndex >= len(displays) {
		return fmt.Errorf("鎵€閫夋樉绀哄櫒鏃犳晥")
	}

	bounds := displays[displayIndex]
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	width, height := resolveShareResolution(sourceWidth, sourceHeight, targetWidth, targetHeight)
	fps = normalizeShareFPS(fps)

	session := &shareSession{
		displayIndex: displayIndex,
		width:        width,
		height:       height,
		fps:          fps,
	}

	a.mu.Lock()
	a.activeShare = session
	a.selfSharing = true
	if a.mediaManager == nil {
		a.mediaManager = media.NewManagerWithConfig(a.room.SelfID(), a.room, media.ManagerConfigFromRelayConfig(a.relayConfig))
		a.mediaManager.SetDiagnosticsChangeHandler(a.emitRuntimeStatus)
	}
	manager := a.mediaManager
	manager.SetKeyFrameRequestHandler(a.requestWebRTCKeyFrame)
	a.mu.Unlock()
	a.emitRuntimeStatus()

	log.Printf("[share/start] self=%s display=%d target=%dx%d fps=%d",
		a.room.SelfID(), displayIndex, width, height, fps)

	audioTargets := a.selectedAudioCaptureTargets()
	audioTarget, audioEnabled := a.prepareAudioCaptureTarget(audioTargets)

	if audioEnabled {
		if err := a.startWebRTCAudioLoop(manager, audioTarget); err != nil {
			log.Printf("[share/audio] start WebRTC audio loop failed: %v", err)
			a.stopAudioMediaLoop()
			audioEnabled = false
		}
	}

	if err := manager.StartShare(a.ctx, media.ShareConfig{
		SessionID:    fmt.Sprintf("share-%s-%d", a.room.SelfID(), time.Now().UnixNano()),
		HostID:       a.room.SelfID(),
		Width:        width,
		Height:       height,
		FPS:          fps,
		Peers:        a.room.GetPeers(),
		AudioEnabled: audioEnabled,
	}); err != nil {
		a.stopMediaLoop()
		manager.StopShare()
		a.mu.Lock()
		if a.mediaManager == manager {
			a.mediaManager = nil
		}
		a.activeShare = nil
		a.selfSharing = false
		a.mu.Unlock()
		return fmt.Errorf("start WebRTC sharing: %w", err)
	}

	if err := a.startWebRTCVideoLoop(manager, session); err != nil {
		a.stopMediaLoop()
		manager.StopShare()
		a.mu.Lock()
		if a.mediaManager == manager {
			a.mediaManager = nil
		}
		a.activeShare = nil
		a.selfSharing = false
		a.mu.Unlock()
		return fmt.Errorf("start WebRTC video loop: %w", err)
	}

	runtime.EventsEmit(a.ctx, string(app.EventStreamStarted), app.StreamEvent{
		PeerID: a.room.SelfID(),
		Width:  width,
		Height: height,
		FPS:    fps,
	})

	if err := a.room.StartSharing(width, height, fps, "webrtc-h264", "webrtc"); err != nil {
		log.Printf("notify WebRTC sharing failed: %v", err)
		a.stopMediaLoop()
		manager.StopShare()
		a.mu.Lock()
		if a.mediaManager == manager {
			a.mediaManager = nil
		}
		a.activeShare = nil
		a.selfSharing = false
		a.mu.Unlock()
		return err
	}
	return nil
}

func (a *App) startWebRTCVideoLoop(manager *media.Manager, session *shareSession) error {
	if manager == nil || session == nil {
		return fmt.Errorf("WebRTC video loop requires an active media session")
	}

	a.stopVideoMediaLoop()

	capturer := video.NewNativeCapturer()
	if err := capturer.Start(session.captureConfig()); err != nil {
		return fmt.Errorf("start native capturer: %w", err)
	}

	encoder := video.NewNativeEncoder()
	encoder.SetInputPixelFormat(video.NativePixelFormatBGRA)
	encoder.SetCapturerHandle(capturer.Handle())
	if err := encoder.Configure(video.EncoderConfig{
		Codec:     video.CodecH264,
		Bitrate:   targetWebRTCBitrateKbps(session.width, session.height, session.fps),
		Width:     session.width,
		Height:    session.height,
		FrameRate: session.fps,
	}); err != nil {
		_ = encoder.Close()
		_ = capturer.Stop()
		return fmt.Errorf("configure native encoder: %w", err)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	a.mu.Lock()
	a.mediaCancel = cancel
	a.mediaDone = done
	a.mu.Unlock()

	go a.runWebRTCVideoLoop(loopCtx, manager, capturer, encoder, session.width, session.height, session.fps, done)
	return nil
}

func (a *App) startWebRTCAudioLoop(manager *media.Manager, target audioCaptureTarget) error {
	if manager == nil {
		return fmt.Errorf("WebRTC audio loop requires an active media manager")
	}
	if target.PID == 0 {
		return fmt.Errorf("WebRTC audio loop requires a process target")
	}

	a.stopAudioMediaLoop()

	opusEncoder, err := audio.NewOpusEncoder(audio.OpusEncoderConfig{
		SampleRate:  audio.DefaultOpusSampleRate,
		Channels:    audio.DefaultOpusChannels,
		FrameMillis: audio.DefaultOpusFrameMillis,
	})
	if err != nil {
		return err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(ctx)

	capturer := windowsaudio.NewProcessCapturer()
	if err := capturer.Start(loopCtx, windowsaudio.ProcessCaptureConfig{
		TargetProcessID: target.PID,
		Mode:            windowsaudio.CaptureIncludeProcessTree,
		SampleRate:      audio.DefaultOpusSampleRate,
		Channels:        audio.DefaultOpusChannels,
		FrameMillis:     audio.DefaultOpusFrameMillis,
	}); err != nil {
		cancel()
		_ = opusEncoder.Close()
		return err
	}

	done := make(chan struct{})
	a.mu.Lock()
	a.audioMediaCancel = cancel
	a.audioMediaDone = done
	a.mu.Unlock()

	log.Printf("[share/audio] started process-loopback audio target=%s:%d codec=opus", target.Name, target.PID)
	go a.runWebRTCAudioLoop(loopCtx, manager, capturer, opusEncoder, done)
	return nil
}

func (a *App) runWebRTCAudioLoop(
	ctx context.Context,
	manager *media.Manager,
	capturer *windowsaudio.ProcessCapturer,
	encoder *audio.OpusEncoder,
	done chan<- struct{},
) {
	defer close(done)
	defer capturer.Stop()
	defer encoder.Close()

	frames := capturer.Frames()
	errors := capturer.Errors()
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errors:
			if ok && err != nil {
				log.Printf("[share/audio] capture failed: %v", err)
			}
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if frame.SampleRate != audio.DefaultOpusSampleRate || frame.Channels != audio.DefaultOpusChannels {
				log.Printf("[share/audio] dropping unexpected PCM format sample_rate=%d channels=%d", frame.SampleRate, frame.Channels)
				continue
			}
			samples, err := audio.PCM16BytesToSamples(frame.Data)
			if err != nil {
				log.Printf("[share/audio] decode PCM16 frame failed: %v", err)
				continue
			}
			packets, err := encoder.EncodePCM16(samples)
			if err != nil {
				log.Printf("[share/audio] opus encode failed: %v", err)
				return
			}
			for _, packet := range packets {
				if err := manager.WriteOpusSample(packet, encoder.FrameDuration()); err != nil {
					log.Printf("[share/audio] write opus sample failed: %v", err)
				}
			}
		}
	}
}

func (a *App) runWebRTCVideoLoop(
	ctx context.Context,
	manager *media.Manager,
	capturer *video.NativeCapturer,
	encoder *video.NativeEncoder,
	targetWidth int,
	targetHeight int,
	fps int,
	done chan<- struct{},
) {
	defer close(done)
	defer func() {
		_ = encoder.Close()
		_ = capturer.Stop()
	}()

	if fps <= 0 {
		fps = defaultShareFPS
	}
	frameDuration := time.Second / time.Duration(fps)
	timeoutMs := int(frameDuration / time.Millisecond)
	if timeoutMs < 5 {
		timeoutMs = 5
	}

	nextFrameAt := time.Now()
	servedKeyFrameSeq := a.mediaKeyFrameSeq.Load()
	directNativePath := true
	directFailures := 0
	cpuEncodeFailures := 0
	var lastFrame *video.Frame
	txStats := newWebRTCVideoTxStats()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !waitForWebRTCFrameSlot(ctx, &nextFrameAt, frameDuration) {
			return
		}
		a.applyWebRTCKeyFrameRequest(encoder, &servedKeyFrameSeq)

		if directNativePath {
			encoded, _, err := encoder.EncodeFromCapturer(capturer, timeoutMs)
			if err == nil {
				directFailures = 0
				if err := manager.WriteH264Sample(encoded, frameDuration); err != nil {
					log.Printf("[media/webrtc] write H264 sample failed: %v", err)
				}
				txStats.record(len(encoded), manager.SessionCount(), "direct")
				continue
			}

			select {
			case <-ctx.Done():
				return
			default:
			}

			directFailures++
			if shouldDisableDirectWebRTCEncode(err, directFailures) {
				log.Printf("[media/webrtc] direct native encode disabled; falling back to CPU resize + native encode: %v", err)
				directNativePath = false
			} else {
				log.Printf("[media/webrtc] encode from native capturer failed: %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		frame, err := capturer.GetFrame()
		if err != nil {
			if reusableWebRTCFrame(lastFrame) {
				frame = cloneWebRTCFrameForReuse(lastFrame, time.Now().UnixMicro())
			} else {
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		if frame == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		if !reusableWebRTCFrame(lastFrame) || frame != lastFrame {
			lastFrame = cloneWebRTCFrameForReuse(frame, frame.Timestamp)
		}
		frame = prepareWebRTCFrameForEncode(frame, targetWidth, targetHeight)

		encoded, err := encoder.Encode(frame)
		if err != nil {
			cpuEncodeFailures++
			if cpuEncodeFailures == 1 || cpuEncodeFailures%60 == 0 {
				log.Printf("[media/webrtc] native encode from CPU frame failed: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		cpuEncodeFailures = 0
		if err := manager.WriteH264Sample(encoded, frameDuration); err != nil {
			log.Printf("[media/webrtc] write H264 sample failed: %v", err)
		}
		txStats.record(len(encoded), manager.SessionCount(), "cpu-resize")
	}
}

func waitForWebRTCFrameSlot(ctx context.Context, nextFrameAt *time.Time, frameDuration time.Duration) bool {
	if frameDuration <= 0 {
		return true
	}
	now := time.Now()
	if nextFrameAt.IsZero() {
		*nextFrameAt = now
	}
	if wait := nextFrameAt.Sub(now); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	now = time.Now()
	if nextFrameAt.Before(now.Add(-frameDuration)) {
		*nextFrameAt = now.Add(frameDuration)
	} else {
		*nextFrameAt = nextFrameAt.Add(frameDuration)
	}
	return true
}

func (a *App) requestWebRTCKeyFrame(peerID string, reason string) {
	seq := a.mediaKeyFrameSeq.Add(1)
	log.Printf("[media/webrtc] keyframe requested peer=%s reason=%s seq=%d", peerID, reason, seq)
}

func (a *App) applyWebRTCKeyFrameRequest(encoder *video.NativeEncoder, servedSeq *int64) {
	if encoder == nil || servedSeq == nil {
		return
	}
	seq := a.mediaKeyFrameSeq.Load()
	if seq == *servedSeq {
		return
	}
	*servedSeq = seq
	if err := encoder.ForceKeyFrame(); err != nil {
		log.Printf("[media/webrtc] force H264 keyframe failed seq=%d: %v", seq, err)
		return
	}
	log.Printf("[media/webrtc] force H264 keyframe seq=%d", seq)
}

type webRTCVideoTxStats struct {
	lastLog          time.Time
	encodedFrames    int
	encodedBytes     int
	sampleWrites     int
	noSessionSamples int
	lastSessions     int
}

func newWebRTCVideoTxStats() *webRTCVideoTxStats {
	return &webRTCVideoTxStats{lastLog: time.Now()}
}

func (s *webRTCVideoTxStats) record(encodedBytes int, sessions int, mode string) {
	s.encodedFrames++
	s.encodedBytes += encodedBytes
	s.lastSessions = sessions
	if sessions > 0 {
		s.sampleWrites += sessions
	} else {
		s.noSessionSamples++
	}

	if time.Since(s.lastLog) < 2*time.Second {
		return
	}
	log.Printf("[media/webrtc] video tx mode=%s encoded_frames=%d encoded_kb=%.1f sample_writes=%d sessions=%d dropped_no_session=%d",
		mode,
		s.encodedFrames,
		float64(s.encodedBytes)/1024.0,
		s.sampleWrites,
		s.lastSessions,
		s.noSessionSamples,
	)
	s.lastLog = time.Now()
	s.encodedFrames = 0
	s.encodedBytes = 0
	s.sampleWrites = 0
	s.noSessionSamples = 0
}

func shouldDisableDirectWebRTCEncode(err error, failures int) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "capture size != encode target") {
		return true
	}
	return failures >= 60
}

func prepareWebRTCFrameForEncode(frame *video.Frame, targetWidth, targetHeight int) *video.Frame {
	if frame == nil {
		return nil
	}
	if targetWidth <= 0 || targetHeight <= 0 || (frame.Width == targetWidth && frame.Height == targetHeight) {
		return frame
	}

	resized := video.ResizeFrame(frame.Data, frame.Width, frame.Height, targetWidth, targetHeight)
	return &video.Frame{
		Data:      resized,
		Timestamp: frame.Timestamp,
		Width:     targetWidth,
		Height:    targetHeight,
	}
}

func reusableWebRTCFrame(frame *video.Frame) bool {
	return frame != nil && frame.Width > 0 && frame.Height > 0 && len(frame.Data) > 0
}

func cloneWebRTCFrameForReuse(frame *video.Frame, timestamp int64) *video.Frame {
	if frame == nil {
		return nil
	}
	return &video.Frame{
		Data:      frame.Data,
		Timestamp: timestamp,
		Width:     frame.Width,
		Height:    frame.Height,
	}
}

func (a *App) stopMediaLoop() {
	a.stopAudioMediaLoop()
	a.stopVideoMediaLoop()
}

func (a *App) stopVideoMediaLoop() {
	a.mu.Lock()
	cancel := a.mediaCancel
	done := a.mediaDone
	a.mediaCancel = nil
	a.mediaDone = nil
	a.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (a *App) stopAudioMediaLoop() {
	a.mu.Lock()
	cancel := a.audioMediaCancel
	done := a.audioMediaDone
	a.audioMediaCancel = nil
	a.audioMediaDone = nil
	a.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func targetWebRTCBitrateKbps(width, height, fps int) int {
	if width <= 0 || height <= 0 {
		return 3500
	}
	if width <= 1280 && height <= 720 && fps <= 30 {
		return 3500
	}
	if fps > 30 {
		return 8000
	}
	return 6000
}

func webRTCBrowserTransportAvailable(net network.Network) (bool, string) {
	switch n := net.(type) {
	case *network.NetworkedNetwork:
		return false, "WebRTC browser playback currently requires tailscale-system or LAN routing; embedded tsnet UDP mux is not implemented yet"
	case *network.AutoNetworkedNetwork:
		if n.ActiveBackend() == "tsnet" {
			return false, "WebRTC browser playback currently requires tailscale-system or LAN routing; active backend is embedded tsnet"
		}
	}
	return true, ""
}

func (a *App) StopSharing() error {
	if a.room == nil {
		return fmt.Errorf("create or join a room first")
	}

	a.mu.Lock()
	isSharing := a.selfSharing
	a.mu.Unlock()
	if !isSharing {
		return fmt.Errorf("screen sharing is not currently active")
	}

	a.stopMediaLoop()

	a.mu.Lock()
	if a.mediaManager != nil {
		a.mediaManager.StopShare()
		a.mediaManager = nil
	}
	for id, s := range a.senders {
		_ = s.Stop()
		delete(a.senders, id)
	}
	for id, r := range a.receivers {
		_ = r.Stop()
		delete(a.receivers, id)
	}
	a.activeShare = nil
	a.selfSharing = false
	a.mu.Unlock()

	if err := a.room.StopSharing(); err != nil {
		log.Printf("stop sharing room state update failed: %v", err)
		return err
	}

	runtime.EventsEmit(a.ctx, string(app.EventStreamStopped), map[string]interface{}{
		"peerId": a.room.SelfID(),
	})
	a.emitRuntimeStatus()
	return nil
}

func (a *App) SendMediaSignal(signal room.MediaSignalPayload) error {
	if a.room == nil {
		return fmt.Errorf("create or join a room first")
	}

	if signal.ToPeerID == "" {
		return fmt.Errorf("media signal target peer is required")
	}
	if signal.FromPeerID == "" {
		signal.FromPeerID = a.room.SelfID()
	}
	if signal.Media == "" {
		signal.Media = "screen"
	}

	if err := a.room.SendMediaSignal(signal.ToPeerID, signal); err != nil {
		log.Printf("[media/signal] send kind=%s session=%s to=%s failed: %v", signal.Kind, signal.SessionID, signal.ToPeerID, err)
		return err
	}
	log.Printf("[media/signal] send kind=%s session=%s from=%s to=%s media=%s",
		signal.Kind, signal.SessionID, signal.FromPeerID, signal.ToPeerID, signal.Media)
	return nil
}

func normalizeShareFPS(fps int) int {
	if fps <= 0 {
		return defaultShareFPS
	}
	if fps > maxShareFPS {
		return maxShareFPS
	}
	return fps
}

func resolveShareResolution(sourceWidth, sourceHeight, targetWidth, targetHeight int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return 1, 1
	}

	if targetWidth <= 0 || targetHeight <= 0 {
		return makeEven(sourceWidth), makeEven(sourceHeight)
	}

	limitedWidth := minInt(targetWidth, sourceWidth)
	limitedHeight := minInt(targetHeight, sourceHeight)
	if limitedWidth <= 0 {
		limitedWidth = sourceWidth
	}
	if limitedHeight <= 0 {
		limitedHeight = sourceHeight
	}

	targetRatio := float64(limitedWidth) / float64(limitedHeight)
	sourceRatio := float64(sourceWidth) / float64(sourceHeight)

	outWidth := limitedWidth
	outHeight := limitedHeight
	if targetRatio > sourceRatio {
		outWidth = int(float64(limitedHeight) * sourceRatio)
	} else {
		outHeight = int(float64(limitedWidth) / sourceRatio)
	}

	if outWidth <= 0 {
		outWidth = 1
	}
	if outHeight <= 0 {
		outHeight = 1
	}

	return makeEven(outWidth), makeEven(outHeight)
}

func makeEven(value int) int {
	if value <= 1 {
		return 1
	}
	if value%2 != 0 {
		value--
	}
	if value < 1 {
		return 1
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *App) GetPeers() []room.PeerInfo {
	if a.room == nil {
		return nil
	}
	return a.room.GetPeers()
}

func (a *App) GetNetworkStatus() app.NetworkStatusEvent {
	return a.networkStatusEvent()
}

func (a *App) GetVideoCapability() app.VideoCapabilityEvent {
	return a.videoCapabilityEvent()
}

func (a *App) GetAudioCapability() app.AudioCapabilityEvent {
	return a.audioCapabilityEvent()
}

func (a *App) GetRuntimeStatus() app.RuntimeStatusEvent {
	return a.runtimeStatusEvent()
}

func (a *App) GetAvailableNodes() []network.NodeInfo {
	return a.network.ListNodes()
}

func (a *App) GetAudioShareCandidates() []AudioShareCandidate {
	sources, err := windowsaudio.ListActiveAudioSources()
	if err != nil {
		log.Printf("list active audio share sessions failed: %v", err)
		return []AudioShareCandidate{}
	}

	a.mu.Lock()
	selection := make(map[string]bool, len(a.audioShareSelection))
	for key, selected := range a.audioShareSelection {
		selection[key] = selected
	}
	a.mu.Unlock()

	return buildAudioShareCandidatesFromAudioSources(sources, selection)
}

func buildAudioShareCandidatesFromAudioSources(sources []windowsaudio.AudioSource, selection map[string]bool) []AudioShareCandidate {
	out := make([]AudioShareCandidate, 0, len(sources))
	seen := make(map[string]bool)
	for _, source := range sources {
		if source.ProcessID == 0 || !source.Active {
			continue
		}
		name := source.Name
		if !isSelectableAudioProcessName(name) {
			continue
		}
		key := normalizeAudioProcessNameKey(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		displayName := strings.TrimSpace(source.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSuffix(name, ".exe")
		}
		out = append(out, AudioShareCandidate{
			PID:         int(source.ProcessID),
			Name:        name,
			DisplayName: displayName,
			IconDataURL: source.IconDataURL,
			Active:      source.Active,
			Selected:    selection[key],
		})
	}

	sort.Slice(out, func(i, j int) bool {
		left := normalizeAudioProcessNameKey(out[i].Name)
		right := normalizeAudioProcessNameKey(out[j].Name)
		if left == right {
			return out[i].PID < out[j].PID
		}
		return left < right
	})
	return out
}

func buildAudioShareCandidates(processes []ps.Process, selection map[string]bool) []AudioShareCandidate {
	out := make([]AudioShareCandidate, 0, len(processes))
	seen := make(map[string]bool)
	for _, proc := range processes {
		if proc == nil || proc.Pid() <= 0 {
			continue
		}
		name := proc.Executable()
		if !isSelectableAudioProcessName(name) {
			continue
		}
		key := normalizeAudioProcessNameKey(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		displayName := strings.TrimSuffix(name, ".exe")
		out = append(out, AudioShareCandidate{
			PID:         proc.Pid(),
			Name:        name,
			DisplayName: displayName,
			Active:      true,
			Selected:    selection[key],
		})
	}

	sort.Slice(out, func(i, j int) bool {
		left := normalizeAudioProcessNameKey(out[i].Name)
		right := normalizeAudioProcessNameKey(out[j].Name)
		if left == right {
			return out[i].PID < out[j].PID
		}
		return left < right
	})
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func (a *App) SetAudioShareSelection(sourceNames []string) {
	cleaned := normalizeAudioShareSelection(sourceNames)
	next := make(map[string]bool, len(cleaned))
	for _, name := range cleaned {
		next[normalizeAudioProcessNameKey(name)] = true
	}

	a.mu.Lock()
	a.audioShareSelection = next
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.AudioShareSources = cleaned
	a.mu.Unlock()
	a.audioShareSession.SetSelected(cleaned)
	a.persistConfig()
}

func (a *App) selectedAudioCaptureTargets() []audioCaptureTarget {
	if a.audioShareSession == nil {
		return nil
	}
	selected := a.audioShareSession.Selected()
	sources, err := windowsaudio.ListActiveAudioSources()
	if err == nil {
		targets := selectedAudioCaptureTargetsFromAudioSources(sources, selected)
		if len(targets) > 0 {
			return targets
		}
	} else {
		log.Printf("resolve active audio capture targets from sessions failed: %v", err)
	}

	processes, err := ps.Processes()
	if err != nil {
		log.Printf("resolve audio capture targets failed: %v", err)
		return nil
	}
	return selectedAudioCaptureTargetsFromProcesses(processes, selected)
}

func (a *App) prepareAudioCaptureTarget(targets []audioCaptureTarget) (audioCaptureTarget, bool) {
	if len(targets) == 0 {
		return audioCaptureTarget{}, false
	}
	if !a.audioCapability.SupportsProcessCapture {
		log.Printf("[share/audio] selected sources unavailable for process capture: %s", a.audioCapability.Reason)
		return audioCaptureTarget{}, false
	}
	opusCapability := audio.QueryOpusCapability()
	if !opusCapability.Available {
		log.Printf("[share/audio] selected process-loopback targets=%s but Opus encoder is unavailable: %s",
			formatAudioCaptureTargets(targets), opusCapability.Reason)
		return audioCaptureTarget{}, false
	}
	return targets[0], true
}

func normalizeAudioShareSelection(sourceNames []string) []string {
	for _, name := range sourceNames {
		canonical := canonicalAudioProcessName(name)
		if canonical != "" {
			return []string{canonical}
		}
	}
	return []string{}
}

func canonicalAudioProcessName(name string) string {
	key := normalizeAudioProcessNameKey(name)
	if key == "" || !isSelectableAudioProcessName(key) {
		return ""
	}
	return key + ".exe"
}

func selectedAudioCaptureTargetsFromProcesses(processes []ps.Process, selectedNames []string) []audioCaptureTarget {
	selected := make(map[string]bool, len(selectedNames))
	for _, name := range selectedNames {
		key := normalizeAudioProcessNameKey(name)
		if key != "" && isSelectableAudioProcessName(name) {
			selected[key] = true
		}
	}
	if len(selected) == 0 {
		return nil
	}

	out := make([]audioCaptureTarget, 0)
	for _, proc := range processes {
		if proc == nil || proc.Pid() <= 0 {
			continue
		}
		name := proc.Executable()
		key := normalizeAudioProcessNameKey(name)
		if !selected[key] || !isSelectableAudioProcessName(name) {
			continue
		}
		out = append(out, audioCaptureTarget{
			PID:  uint32(proc.Pid()),
			Name: name,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].PID < out[j].PID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func selectedAudioCaptureTargetsFromAudioSources(sources []windowsaudio.AudioSource, selectedNames []string) []audioCaptureTarget {
	selected := make(map[string]bool, len(selectedNames))
	for _, name := range selectedNames {
		key := normalizeAudioProcessNameKey(name)
		if key != "" && isSelectableAudioProcessName(name) {
			selected[key] = true
		}
	}
	if len(selected) == 0 {
		return nil
	}

	out := make([]audioCaptureTarget, 0)
	seenPID := make(map[uint32]bool)
	for _, source := range sources {
		if source.ProcessID == 0 || !source.Active {
			continue
		}
		key := normalizeAudioProcessNameKey(source.Name)
		if !selected[key] || !isSelectableAudioProcessName(source.Name) || seenPID[source.ProcessID] {
			continue
		}
		seenPID[source.ProcessID] = true
		out = append(out, audioCaptureTarget{
			PID:  source.ProcessID,
			Name: source.Name,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].PID < out[j].PID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func formatAudioCaptureTargets(targets []audioCaptureTarget) string {
	if len(targets) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, fmt.Sprintf("%s:%d", target.Name, target.PID))
	}
	return strings.Join(parts, ",")
}

func isSelectableAudioProcessName(name string) bool {
	normalized := normalizeAudioProcessNameKey(name)
	if normalized == "" {
		return false
	}

	if _, blocked := audioProcessBlocklist()[normalized]; blocked {
		return false
	}
	return true
}

func normalizeAudioProcessNameKey(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(normalized, ".exe")
}

func audioProcessBlocklist() map[string]bool {
	return map[string]bool{
		"applicationframehost":    true,
		"audiodg":                 true,
		"conhost":                 true,
		"csrss":                   true,
		"ctfmon":                  true,
		"dllhost":                 true,
		"dwm":                     true,
		"explorer":                true,
		"fontdrvhost":             true,
		"lockapp":                 true,
		"lsass":                   true,
		"partyLAN":                true,
		"partylan":                true,
		"registry":                true,
		"runtimebroker":           true,
		"searchhost":              true,
		"securityhealthsystray":   true,
		"services":                true,
		"settingSyncHost":         true,
		"settingsynchost":         true,
		"shellexperiencehost":     true,
		"smartscreen":             true,
		"smss":                    true,
		"spoolsv":                 true,
		"startmenuexperiencehost": true,
		"svchost":                 true,
		"system":                  true,
		"systemsettings":          true,
		"taskhostw":               true,
		"textinputhost":           true,
		"wininit":                 true,
		"winlogon":                true,
		"winstore.app":            true,
		"wudfhost":                true,

		"discord":         true,
		"ms-teams":        true,
		"teams":           true,
		"teamspeak":       true,
		"teamspeak3":      true,
		"ts3client_win32": true,
		"ts3client_win64": true,
		"zoom":            true,
		"zoomus":          true,
		"qq":              true,
		"wechat":          true,
		"wechatapp":       true,
		"weixin":          true,
	}
}

func (a *App) StartVoiceChat() (VoiceStateSnapshot, error) {
	if a.room == nil {
		return a.GetVoiceState(), fmt.Errorf("create or join a room first")
	}

	peerID := a.selectVoicePeerID()
	if peerID == "" {
		return a.GetVoiceState(), fmt.Errorf("voice peer is not available")
	}

	a.mu.Lock()
	if a.voiceActive {
		snapshot := a.voiceStateSnapshotLocked()
		a.mu.Unlock()
		return snapshot, nil
	}
	volume := a.voiceVolume
	if volume < 0 {
		volume = 0.8
	}
	a.mu.Unlock()

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	ch := voice.NewVoiceChannel(a.network)
	ch.SetVolume(volume)
	ch.OnStateChange(func(peerID string, state voice.VoiceState) {
		a.emitVoiceState(peerID, int(state))
	})

	if err := ch.Start(ctx, peerID); err != nil {
		return a.GetVoiceState(), fmt.Errorf("start voice chat: %w", err)
	}

	a.mu.Lock()
	a.voiceChannel = ch
	a.voiceActive = true
	a.voiceMuted = false
	a.voiceVolume = volume
	a.voicePeerID = peerID
	snapshot := a.voiceStateSnapshotLocked()
	a.mu.Unlock()

	a.emitVoiceState(peerID, int(voice.VoiceStateIdle))
	return snapshot, nil
}

func (a *App) StopVoiceChat() (VoiceStateSnapshot, error) {
	ch, peerID := a.detachVoiceChannel()
	if ch != nil {
		if err := ch.Stop(); err != nil {
			return a.GetVoiceState(), fmt.Errorf("stop voice chat: %w", err)
		}
	}
	if peerID != "" {
		a.emitVoiceState(peerID, int(voice.VoiceStateIdle))
	}
	return a.GetVoiceState(), nil
}

func (a *App) SetVoiceMuted(muted bool) VoiceStateSnapshot {
	a.mu.Lock()
	a.voiceMuted = muted
	ch := a.voiceChannel
	active := a.voiceActive
	a.mu.Unlock()

	if active && ch != nil {
		ch.Mute(muted)
	}
	return a.GetVoiceState()
}

func (a *App) SetVoiceVolume(volume int) VoiceStateSnapshot {
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}

	scaled := float32(volume) / 100
	a.mu.Lock()
	a.voiceVolume = scaled
	ch := a.voiceChannel
	a.mu.Unlock()

	if ch != nil {
		ch.SetVolume(scaled)
	}
	return a.GetVoiceState()
}

func (a *App) GetVoiceState() VoiceStateSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.voiceStateSnapshotLocked()
}

func (a *App) selectVoicePeerID() string {
	if a.room == nil {
		return ""
	}
	selfID := a.room.SelfID()
	for _, peer := range a.room.GetPeers() {
		if peer.ID != "" && peer.ID != selfID {
			return peer.ID
		}
	}
	return selfID
}

func (a *App) voiceStateSnapshotLocked() VoiceStateSnapshot {
	volume := int(a.voiceVolume * 100)
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}

	state := int(voice.VoiceStateIdle)
	if a.voiceActive && a.voiceChannel != nil {
		state = int(a.voiceChannel.GetState())
	}
	if a.voiceMuted {
		state = int(voice.VoiceStateMuted)
	}

	message := "Experimental voice controls are connected; real device input and output are not complete yet."
	if a.voiceActive {
		message = "Experimental voice channel is active for UI lifecycle testing."
	}

	return VoiceStateSnapshot{
		Active:       a.voiceActive,
		Muted:        a.voiceMuted,
		Volume:       volume,
		State:        state,
		PeerID:       a.voicePeerID,
		Experimental: true,
		Message:      message,
	}
}

func (a *App) detachVoiceChannel() (voice.VoiceChannel, string) {
	a.mu.Lock()
	ch := a.voiceChannel
	peerID := a.voicePeerID
	a.voiceChannel = nil
	a.voiceActive = false
	a.voiceMuted = false
	a.voicePeerID = ""
	a.mu.Unlock()
	return ch, peerID
}

func (a *App) persistConfig() {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	if cfg == nil {
		return
	}
	if err := config.Save(cfg); err != nil {
		log.Printf("save config failed: %v", err)
	}
}

func (a *App) GetTailscaleAuthKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ""
	}
	return a.cfg.TailscaleAuthKey
}

func (a *App) GetUserPreferences() UserPreferences {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return UserPreferences{}
	}
	return UserPreferences{
		DisplayName: normalizeDisplayName(a.cfg.DisplayName),
	}
}

func (a *App) SetUserPreferences(preferences UserPreferences) error {
	displayName := normalizeDisplayName(preferences.DisplayName)

	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.DisplayName = displayName
	cfg := a.cfg
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}
	return nil
}

func (a *App) SetTailscaleAuthKey(authKey string) error {
	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.TailscaleAuthKey = authKey
	cfg := a.cfg
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save auth key: %w", err)
	}

	if appmode.ResolveMode() != appmode.ModeNetworked {
		return nil
	}

	if a.room != nil {
		return fmt.Errorf("璇峰厛绂诲紑鎴块棿锛屽啀閲嶆柊搴旂敤璁よ瘉瀵嗛挜")
	}

	if err := a.restartNetwork(authKey); err != nil {
		return fmt.Errorf("restart network: %w", err)
	}
	return nil
}

func (a *App) configuredDisplayName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ""
	}
	return normalizeDisplayName(a.cfg.DisplayName)
}

func normalizeDisplayName(name string) string {
	normalized := strings.Join(strings.Fields(name), " ")
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) > maxDisplayNameRunes {
		normalized = string(runes[:maxDisplayNameRunes])
	}
	return normalized
}

func (a *App) restartNetwork(authKey string) error {
	if a.ctx == nil {
		return fmt.Errorf("application context not ready")
	}

	if err := a.network.Stop(); err != nil {
		log.Printf("network stop failed during restart: %v", err)
	}

	startCtx, cancel := context.WithTimeout(a.ctx, networkStartTimeout(authKey))
	defer cancel()

	if err := a.network.Start(startCtx, authKey); err != nil {
		a.emitRuntimeStatus()
		return err
	}

	a.emitNetworkStatus()
	a.emitRuntimeStatus()
	return nil
}

func (a *App) GetRoomState() string {
	if a.room == nil {
		return "closed"
	}
	switch a.room.GetState() {
	case room.RoomStateWaiting:
		return "waiting"
	case room.RoomStateActive:
		return "active"
	default:
		return "closed"
	}
}

func (a *App) IsSharing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selfSharing
}

func (a *App) attachShareToPeer(peer room.PeerInfo) error {
	if a.room == nil {
		return fmt.Errorf("room not ready")
	}
	if peer.ID == "" || peer.ID == a.room.SelfID() {
		return nil
	}

	a.mu.Lock()
	session := a.activeShare
	if session == nil {
		a.mu.Unlock()
		return nil
	}
	manager := a.mediaManager
	a.mu.Unlock()

	if manager == nil {
		return fmt.Errorf("WebRTC media manager is not active")
	}
	if err := manager.AttachViewer(a.ctx, peer); err != nil {
		return fmt.Errorf("attach WebRTC share to peer %s: %w", peer.ID, err)
	}
	if err := a.room.NotifyPeerSharing(peer.ID, session.width, session.height, session.fps, "webrtc-h264", "webrtc"); err != nil {
		return fmt.Errorf("notify peer %s of active WebRTC sharing: %w", peer.ID, err)
	}
	log.Printf("[share/send] peer=%s backend=webrtc target=%dx%d fps=%d", peer.ID, session.width, session.height, session.fps)
	return nil
}

func (a *App) setupRoomCallbacks(r *room.RoomImpl) {
	r.OnPeerJoin(func(peer room.PeerInfo) {
		runtime.EventsEmit(a.ctx, string(app.EventPeerJoined), app.PeerEvent{
			PeerID:   peer.ID,
			Hostname: peer.Hostname,
		})

		a.mu.Lock()
		isSharing := a.selfSharing
		a.mu.Unlock()
		if isSharing {
			go func() {
				if err := a.attachShareToPeer(peer); err != nil {
					log.Printf("auto-attach share to peer %s: %v", peer.ID, err)
				}
			}()
		}
	})

	r.OnPeerLeave(func(peerID string) {
		a.mu.Lock()
		manager := a.mediaManager
		if s, ok := a.senders[peerID]; ok {
			_ = s.Stop()
			delete(a.senders, peerID)
		}
		if rcv, ok := a.receivers[peerID]; ok {
			_ = rcv.Stop()
			delete(a.receivers, peerID)
		}
		a.mu.Unlock()
		if manager != nil {
			manager.DetachViewer(peerID)
		}

		runtime.EventsEmit(a.ctx, string(app.EventPeerLeft), app.PeerEvent{
			PeerID: peerID,
		})
	})

	r.OnStreamStart(func(peerID string, width, height, fps int, codec, backend string) {
		if backend != "webrtc" {
			go a.startReceiving(peerID, width, height, codec, backend)
		}
		runtime.EventsEmit(a.ctx, string(app.EventStreamStarted), app.StreamEvent{
			PeerID: peerID,
			Width:  width,
			Height: height,
			FPS:    fps,
		})
	})

	r.OnStreamStop(func(peerID string) {
		a.mu.Lock()
		if rcv, ok := a.receivers[peerID]; ok {
			_ = rcv.Stop()
			delete(a.receivers, peerID)
		}
		a.mu.Unlock()

		runtime.EventsEmit(a.ctx, string(app.EventStreamStopped), app.StreamEvent{
			PeerID: peerID,
			Width:  0,
			Height: 0,
			FPS:    0,
		})
	})

	r.OnMediaSignal(func(signal room.MediaSignalPayload) {
		log.Printf("[media/signal] recv kind=%s session=%s from=%s to=%s media=%s",
			signal.Kind, signal.SessionID, signal.FromPeerID, signal.ToPeerID, signal.Media)
		a.mu.Lock()
		manager := a.mediaManager
		a.mu.Unlock()
		if manager != nil {
			if err := manager.HandleSignal(signal); err == nil {
				return
			} else if !errors.Is(err, media.ErrUnhandledSignal) {
				log.Printf("[media/signal] handle failed: %v", err)
			}
		}
		runtime.EventsEmit(a.ctx, string(app.EventMediaSignal), signal)
	})

	r.OnFatal(func(err error) {
		log.Printf("room lifecycle failed: %v", err)

		voiceCh, voicePeerID := a.detachVoiceChannel()
		if voiceCh != nil {
			_ = voiceCh.Stop()
		}
		if voicePeerID != "" {
			a.emitVoiceState(voicePeerID, int(voice.VoiceStateIdle))
		}

		a.mu.Lock()
		for id, s := range a.senders {
			_ = s.Stop()
			delete(a.senders, id)
		}
		for id, rcv := range a.receivers {
			_ = rcv.Stop()
			delete(a.receivers, id)
		}
		a.activeShare = nil
		a.selfSharing = false
		a.mu.Unlock()

		if a.room != r {
			return
		}
		a.room = nil
		runtime.EventsEmit(a.ctx, string(app.EventRoomLeft), nil)
		a.emitRuntimeStatus()
		a.emitError(app.ErrCodeRoomInvalidated, "褰撳墠鎴块棿杩炴帴宸插け鏁堬紝璇烽噸鏂板垱寤烘垨鍔犲叆鎴块棿")
	})
}

func (a *App) networkStatusLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.emitNetworkStatus()
		}
	}
}

func (a *App) emitNetworkStatus() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventNetworkStatus), a.networkStatusEvent())
}

func (a *App) networkStatusEvent() app.NetworkStatusEvent {
	ip := ""
	if nodeIP := a.network.GetIP(); nodeIP != nil {
		ip = nodeIP.String()
	}

	return app.NetworkStatusEvent{
		NodeID:         a.network.GetNodeID(),
		IP:             ip,
		PeerCount:      len(a.network.ListNodes()),
		ConnectionType: string(appmode.ResolveMode()),
		Latency:        0,
	}
}

func (a *App) emitError(code int, message string) {
	if a.ctx == nil {
		return
	}

	runtime.EventsEmit(a.ctx, string(app.EventError), app.ErrorEvent{
		Code:    code,
		Message: message,
	})
}

func (a *App) emitVoiceState(peerID string, state int) {
	if a.ctx == nil {
		return
	}
	if peerID == "" && a.room != nil {
		peerID = a.room.SelfID()
	}
	if peerID == "" {
		return
	}

	runtime.EventsEmit(a.ctx, string(app.EventPeerVoiceState), app.VoiceStateEvent{
		PeerID: peerID,
		State:  state,
	})
}

func (a *App) emitVideoCapability() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventVideoCapability), a.videoCapabilityEvent())
}

func (a *App) videoCapabilityEvent() app.VideoCapabilityEvent {
	return app.VideoCapabilityEvent{
		Backend:      string(a.hostVideoCapability.Backend),
		Status:       string(a.hostVideoCapability.Status),
		Adapter:      a.hostVideoCapability.Adapter,
		Driver:       a.hostVideoCapability.Driver,
		Reason:       a.hostVideoCapability.Reason,
		SupportsHost: a.hostVideoCapability.SupportsHost,
		SupportsView: a.hostVideoCapability.SupportsView,
	}
}

func (a *App) emitAudioCapability() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventAudioCapability), a.audioCapabilityEvent())
}

func (a *App) audioCapabilityEvent() app.AudioCapabilityEvent {
	return app.AudioCapabilityEvent{
		SupportsProcessCapture: a.audioCapability.SupportsProcessCapture,
		Reason:                 a.audioCapability.Reason,
	}
}

func (a *App) emitRuntimeStatus() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventRuntimeStatus), a.runtimeStatusEvent())
}

func (a *App) runtimeStatusEvent() app.RuntimeStatusEvent {
	runtimeInfo := a.network.GetRuntimeInfo()
	mediaHealth := a.mediaRuntimeHealth()

	return app.RuntimeStatusEvent{
		Mode:           string(appmode.ResolveMode()),
		NetworkBackend: describeNetworkBackend(a.network),
		NetworkState:   runtimeInfo.BackendState,
		NetworkAuthURL: runtimeInfo.AuthURL,
		NetworkHealth:  runtimeInfo.Health,
		MediaHealth:    mediaHealth,
		VideoBackend:   string(a.hostVideoCapability.Backend),
		VideoStatus:    string(a.hostVideoCapability.Status),
		AudioBackend:   audioBackendLabel(a.audioCapability),
		AudioStatus:    audioStatusLabel(a.audioCapability),
		Version:        version.Version,
	}
}

func (a *App) mediaRuntimeHealth() []string {
	a.mu.Lock()
	manager := a.mediaManager
	relayConfig := a.relayConfig
	a.mu.Unlock()

	if manager != nil {
		return manager.Diagnostics()
	}
	return relayConfig.MediaHealthLines()
}

func audioBackendLabel(cap windowsaudio.Capability) string {
	if cap.SupportsProcessCapture {
		return "app-audio"
	}
	if cap.Reason != "" {
		return "app-audio"
	}
	return "audio-unavailable"
}

func audioStatusLabel(cap windowsaudio.Capability) string {
	if cap.SupportsProcessCapture {
		return "available"
	}
	if cap.Reason != "" {
		return "unavailable"
	}
	return "unavailable"
}

func describeNetworkBackend(net network.Network) string {
	switch n := net.(type) {
	case *network.LoopbackNetwork:
		return "loopback"
	case *network.LANNetwork:
		return "lan"
	case *network.TailscaleSystemNetwork:
		return "tailscale-system"
	case *network.NetworkedNetwork:
		return "networked"
	case *network.AutoNetworkedNetwork:
		backend := n.ActiveBackend()
		if backend == "" {
			return "networked"
		}
		return "networked/" + backend
	default:
		return "unknown"
	}
}

func (a *App) startReceiving(peerID string, width, height int, codec, backend string) {
	videoListener := a.room.VideoListener()
	if videoListener == nil {
		return
	}

	conn, err := videoListener.Accept()
	if err != nil {
		log.Printf("accept video from %s: %v", peerID, err)
		return
	}

	receiver := room.NewStreamReceiverForStream(conn, width, height, codec, backend)
	if err := receiver.Start(); err != nil {
		log.Printf("start receiver for %s: %v", peerID, err)
		_ = conn.Close()
		return
	}
	log.Printf("[share/recv] peer=%s frame=%dx%d", peerID, width, height)
	log.Printf("[share/recv] peer=%s codec=%s stream_backend=%s", peerID, codec, backend)
	log.Printf("[share/recv] peer=%s backend=%s", peerID, receiver.BackendName())

	a.mu.Lock()
	a.receivers[peerID] = receiver
	a.mu.Unlock()

	go func() {
		var lastPreviewEmit time.Time
		for frame := range receiver.Frames() {
			now := time.Now()
			if previewUIFPS > 0 {
				minInterval := time.Second / time.Duration(previewUIFPS)
				if !lastPreviewEmit.IsZero() && now.Sub(lastPreviewEmit) < minInterval {
					continue
				}
				lastPreviewEmit = now
			}

			previewData, err := encodePreviewJPEG(frame, 72)
			if err != nil {
				log.Printf("encode preview frame for %s: %v", peerID, err)
				continue
			}
			stats := receiver.RuntimeStats()
			runtime.EventsEmit(a.ctx, string(app.EventStreamFrame), map[string]interface{}{
				"peerId":              peerID,
				"width":               frame.Width,
				"height":              frame.Height,
				"timestamp":           frame.Timestamp,
				"format":              "jpeg",
				"data":                base64.StdEncoding.EncodeToString(previewData),
				"decodedFps":          stats.DecodedFPS,
				"estimatedSourceFps":  stats.EstimatedSourceFPS,
				"incomingBitrateKbps": stats.IncomingBitrateKbps,
			})
		}
	}()
}

func encodePreviewJPEG(frame *video.Frame, quality int) ([]byte, error) {
	if frame == nil {
		return nil, fmt.Errorf("nil frame")
	}

	if quality <= 0 || quality > 100 {
		quality = 72
	}

	buf := &bytes.Buffer{}
	img := &image.RGBA{
		Pix:    frame.Data,
		Stride: frame.Width * 4,
		Rect:   image.Rect(0, 0, frame.Width, frame.Height),
	}

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
