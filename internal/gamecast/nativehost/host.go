package nativehost

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/gamecast"
	gamecastws "PartyLAN/internal/gamecast/ws"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

type Config struct {
	Profile           gamecast.Profile
	RoomID            string
	Name              string
	Width             int
	Height            int
	FPS               int
	AudioEnabled      bool
	OnAuxMessage      func(json.RawMessage)
	OnStatus          func(gamecastws.Status)
	OnKeyFrameRequest func(reason string)
}

type VideoLayer struct {
	RID         string
	Width       int
	Height      int
	FPS         int
	BitrateKbps int
}

type Host struct {
	config Config

	mu                sync.Mutex
	ws                *gamecastws.Client
	pc                *webrtc.PeerConnection
	videoTracks       map[string]*webrtc.TrackLocalStaticSample
	videoLayers       []VideoLayer
	videoLayerViewers map[string]int
	presenceReceived  bool
	audioTrack        *webrtc.TrackLocalStaticSample
	pendingICE        []webrtc.ICECandidateInit
	cancel            context.CancelFunc
	closed            bool
}

func New(config Config) *Host {
	return &Host{
		config:            config,
		videoLayerViewers: make(map[string]int),
	}
}

func (h *Host) Start(parent context.Context) error {
	roomID := strings.TrimSpace(h.config.RoomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)
	h.mu.Lock()
	h.videoLayerViewers = make(map[string]int)
	h.presenceReceived = false
	h.mu.Unlock()

	wsClient := gamecastws.NewClient(gamecastws.Options{
		Profile: h.config.Profile,
		RoomID:  roomID,
		Role:    "host",
		Name:    h.config.Name,
		OnMessage: func(message json.RawMessage) {
			if err := h.handleSignal(message); err != nil {
				log.Printf("[gamecast/native-host] handle signal failed: %v", err)
			}
		},
		OnStatus: h.emitStatus,
	})
	if err := wsClient.Connect(runCtx); err != nil {
		cancel()
		return err
	}

	pc, tracks, layers, audioTrack, err := h.createPeerConnection(runCtx, wsClient)
	if err != nil {
		cancel()
		_ = wsClient.Close()
		return err
	}

	h.mu.Lock()
	h.ws = wsClient
	h.pc = pc
	h.videoTracks = tracks
	h.videoLayers = layers
	h.audioTrack = audioTrack
	h.cancel = cancel
	h.closed = false
	h.pendingICE = nil
	h.mu.Unlock()

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = h.Close()
		return fmt.Errorf("create gamecast host offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = h.Close()
		return fmt.Errorf("set gamecast host local description: %w", err)
	}
	if err := h.sendJSON(runCtx, map[string]interface{}{
		"type": "offer",
		"sdp":  offer.SDP,
	}); err != nil {
		_ = h.Close()
		return err
	}
	h.emitStatus(gamecastws.Status{State: "connecting", Detail: "native host offer sent"})
	return nil
}

func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	cancel := h.cancel
	wsClient := h.ws
	pc := h.pc
	h.cancel = nil
	h.ws = nil
	h.pc = nil
	h.videoTracks = nil
	h.videoLayers = nil
	h.videoLayerViewers = nil
	h.presenceReceived = false
	h.audioTrack = nil
	h.pendingICE = nil
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if pc != nil {
		_ = pc.Close()
	}
	if wsClient != nil {
		if err := wsClient.Close(); err != nil {
			return err
		}
	}
	h.emitStatus(gamecastws.Status{State: "closed", Detail: "native host stopped"})
	return nil
}

func (h *Host) WriteH264Sample(data []byte, duration time.Duration) error {
	return h.WriteH264LayerSample("f", data, duration)
}

func (h *Host) WriteH264LayerSample(rid string, data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}
	h.mu.Lock()
	track := h.videoTracks[normalizeVideoLayerRID(rid)]
	h.mu.Unlock()
	if track == nil {
		return nil
	}
	return track.WriteSample(pionmedia.Sample{
		Data:     data,
		Duration: duration,
	})
}

func (h *Host) WriteOpusSample(data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}
	h.mu.Lock()
	track := h.audioTrack
	h.mu.Unlock()
	if track == nil {
		return nil
	}
	return track.WriteSample(pionmedia.Sample{
		Data:     data,
		Duration: duration,
	})
}

func (h *Host) SessionCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pc == nil || len(h.videoTracks) == 0 {
		return 0
	}
	count := 0
	for _, viewers := range h.videoLayerViewers {
		count += viewers
	}
	return count
}

func (h *Host) ActiveVideoLayers() []VideoLayer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.videoLayers) == 0 {
		return nil
	}
	// The native host publishes one full-quality track. Viewer layer requests
	// must not disable it when they still carry the legacy q/h/f preference.
	if len(h.videoLayers) == 1 {
		return append([]VideoLayer(nil), h.videoLayers...)
	}
	viewerCount := 0
	for _, count := range h.videoLayerViewers {
		viewerCount += count
	}
	if !h.presenceReceived || viewerCount == 0 {
		return append([]VideoLayer(nil), h.videoLayers...)
	}
	active := make([]VideoLayer, 0, len(h.videoLayers))
	for _, layer := range h.videoLayers {
		if h.videoLayerViewers[layer.RID] > 0 {
			active = append(active, layer)
		}
	}
	return active
}

func (h *Host) createPeerConnection(ctx context.Context, wsClient *gamecastws.Client) (*webrtc.PeerConnection, map[string]*webrtc.TrackLocalStaticSample, []VideoLayer, *webrtc.TrackLocalStaticSample, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, nil, nil, nil, err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: gamecast.STUNURLs(h.config.Profile.BaseURL)},
		},
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	layers := SingleVideoLayer(h.config.Width, h.config.Height, h.config.FPS)
	streamID := fmt.Sprintf("gamecast-native-%d", time.Now().UnixNano())
	tracks := make(map[string]*webrtc.TrackLocalStaticSample, len(layers))
	for _, layer := range layers {
		track, trackErr := newSingleH264VideoTrack(layer, streamID)
		if trackErr != nil {
			_ = pc.Close()
			return nil, nil, nil, nil, trackErr
		}
		tracks[layer.RID] = track
	}

	sender, err := pc.AddTrack(tracks["f"])
	if err != nil {
		_ = pc.Close()
		return nil, nil, nil, nil, err
	}
	go h.readSenderRTCP(sender)

	var audioTrack *webrtc.TrackLocalStaticSample
	if h.config.AudioEnabled {
		audioTrack, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		}, "share-app-audio", fmt.Sprintf("gamecast-native-audio-%d", time.Now().UnixNano()))
		if err != nil {
			_ = pc.Close()
			return nil, nil, nil, nil, err
		}
		audioSender, err := pc.AddTrack(audioTrack)
		if err != nil {
			_ = pc.Close()
			return nil, nil, nil, nil, err
		}
		go h.readSenderRTCP(audioSender)
	}

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		if err := h.sendJSON(ctx, map[string]interface{}{
			"type": "ice-candidate",
			"ice":  candidate.ToJSON(),
		}); err != nil {
			log.Printf("[gamecast/native-host] send ICE failed: %v", err)
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[gamecast/native-host] connection_state=%s", state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			h.requestKeyFrame("connection-connected")
			h.emitStatus(gamecastws.Status{State: "connected", Detail: "native host sending media"})
		case webrtc.PeerConnectionStateFailed:
			h.emitStatus(gamecastws.Status{State: "error", Detail: "native host WebRTC connection failed"})
		case webrtc.PeerConnectionStateClosed:
			h.emitStatus(gamecastws.Status{State: "closed", Detail: "native host peer closed"})
		}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[gamecast/native-host] ice_state=%s", state.String())
	})
	return pc, tracks, layers, audioTrack, nil
}

func newSingleH264VideoTrack(layer VideoLayer, streamID string) (*webrtc.TrackLocalStaticSample, error) {
	return webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: h264SDPFmtpLine(layer.Width, layer.Height, layer.FPS),
	}, "screen", streamID,
		webrtc.WithPayloader(func(_ webrtc.RTPCodecCapability) (rtp.Payloader, error) {
			return &codecs.H264Payloader{DisableStapA: true}, nil
		}),
	)
}

func (h *Host) readSenderRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		n, _, err := sender.Read(buf)
		if err != nil {
			return
		}
		packets, err := rtcp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		for _, packet := range packets {
			switch packet.(type) {
			case *rtcp.PictureLossIndication:
				h.requestKeyFrame("rtcp-pli")
			case *rtcp.FullIntraRequest:
				h.requestKeyFrame("rtcp-fir")
			}
		}
	}
}

func (h *Host) handleSignal(raw json.RawMessage) error {
	var header struct {
		Type  string          `json:"type"`
		SDP   json.RawMessage `json:"sdp"`
		ICE   json.RawMessage `json:"ice"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	if header.Error != "" {
		h.emitStatus(gamecastws.Status{State: "error", Detail: header.Error})
		return nil
	}

	switch header.Type {
	case "answer":
		answer, err := parseSessionDescription(header.SDP, webrtc.SDPTypeAnswer)
		if err != nil {
			return err
		}
		return h.acceptAnswer(answer)
	case "ice-candidate":
		var candidate webrtc.ICECandidateInit
		if err := json.Unmarshal(header.ICE, &candidate); err != nil {
			return err
		}
		return h.addICECandidate(candidate)
	case "presence":
		h.updateVideoLayerPresence(raw)
		if h.config.OnAuxMessage != nil {
			h.config.OnAuxMessage(json.RawMessage(append([]byte(nil), raw...)))
		}
		return nil
	default:
		if h.config.OnAuxMessage != nil && header.Type != "" {
			h.config.OnAuxMessage(json.RawMessage(append([]byte(nil), raw...)))
		}
		return nil
	}
}

func (h *Host) updateVideoLayerPresence(raw json.RawMessage) {
	var payload struct {
		Viewers []struct {
			Layer string `json:"layer"`
		} `json:"viewers"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	counts := make(map[string]int, len(payload.Viewers))
	for _, viewer := range payload.Viewers {
		counts[normalizeVideoLayerRID(viewer.Layer)]++
	}
	h.mu.Lock()
	h.videoLayerViewers = counts
	h.presenceReceived = true
	h.mu.Unlock()
}

func SingleVideoLayer(width, height, fps int) []VideoLayer {
	if width <= 0 || height <= 0 {
		width, height = 1280, 720
	}
	if fps <= 0 {
		fps = 30
	}
	fullWidth, fullHeight := evenDimensions(width, height)
	return []VideoLayer{
		{RID: "f", Width: fullWidth, Height: fullHeight, FPS: fps, BitrateKbps: fullLayerBitrateKbps(fullWidth, fullHeight, fps)},
	}
}

func normalizeVideoLayerRID(rid string) string {
	switch strings.ToLower(strings.TrimSpace(rid)) {
	case "q", "h", "f":
		return strings.ToLower(strings.TrimSpace(rid))
	default:
		return "f"
	}
}

func evenDimensions(width, height int) (int, int) {
	width = maxInt(width, 2)
	height = maxInt(height, 2)
	if width%2 != 0 {
		width--
	}
	if height%2 != 0 {
		height--
	}
	return width, height
}

func fullLayerBitrateKbps(width, height, fps int) int {
	switch {
	case width <= 640 && height <= 360 && fps <= 18:
		return 600
	case width <= 640 && height <= 360 && fps <= 24:
		return 900
	case width <= 960 && height <= 540 && fps <= 24:
		return 1700
	case width <= 1280 && height <= 720 && fps <= 24:
		return 3000
	case width <= 1280 && height <= 720 && fps <= 30:
		return 3500
	case width <= 1920 && height <= 1080 && fps <= 30:
		return 6000
	case width <= 1920 && height <= 1080 && fps <= 45:
		return 7000
	case fps > 45:
		return 8000
	default:
		return 6000
	}
}

func (h *Host) acceptAnswer(answer webrtc.SessionDescription) error {
	h.mu.Lock()
	pc := h.pc
	h.mu.Unlock()
	if pc == nil {
		return fmt.Errorf("native host peer is closed")
	}
	if err := pc.SetRemoteDescription(answer); err != nil {
		return err
	}

	h.mu.Lock()
	pending := append([]webrtc.ICECandidateInit(nil), h.pendingICE...)
	h.pendingICE = nil
	h.mu.Unlock()
	for _, candidate := range pending {
		if err := pc.AddICECandidate(candidate); err != nil {
			log.Printf("[gamecast/native-host] add pending ICE failed: %v", err)
		}
	}
	return nil
}

func (h *Host) addICECandidate(candidate webrtc.ICECandidateInit) error {
	h.mu.Lock()
	pc := h.pc
	if pc == nil {
		h.mu.Unlock()
		return nil
	}
	if pc.RemoteDescription() == nil {
		h.pendingICE = append(h.pendingICE, candidate)
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	return pc.AddICECandidate(candidate)
}

func (h *Host) sendJSON(ctx context.Context, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	h.mu.Lock()
	wsClient := h.ws
	h.mu.Unlock()
	if wsClient == nil {
		return fmt.Errorf("gamecast native host websocket is not connected")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsClient.Send(sendCtx, body)
}

func (h *Host) emitStatus(status gamecastws.Status) {
	if h.config.OnStatus != nil {
		h.config.OnStatus(status)
	}
}

func (h *Host) requestKeyFrame(reason string) {
	if h.config.OnKeyFrameRequest != nil {
		h.config.OnKeyFrameRequest(reason)
	}
}

func parseSessionDescription(raw json.RawMessage, fallback webrtc.SDPType) (webrtc.SessionDescription, error) {
	var sdp string
	if err := json.Unmarshal(raw, &sdp); err == nil && strings.TrimSpace(sdp) != "" {
		return webrtc.SessionDescription{Type: fallback, SDP: sdp}, nil
	}
	var desc webrtc.SessionDescription
	if err := json.Unmarshal(raw, &desc); err != nil {
		return webrtc.SessionDescription{}, err
	}
	if desc.Type == webrtc.SDPTypeUnknown {
		desc.Type = fallback
	}
	if strings.TrimSpace(desc.SDP) == "" {
		return webrtc.SessionDescription{}, fmt.Errorf("empty session description")
	}
	return desc, nil
}

func h264SDPFmtpLine(width, height, fps int) string {
	return fmt.Sprintf(
		"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e0%s",
		h264LevelIDHex(width, height, fps),
	)
}

func h264LevelIDHex(width, height, fps int) string {
	mbWidth := (maxInt(width, 16) + 15) / 16
	mbHeight := (maxInt(height, 16) + 15) / 16
	mbPerFrame := mbWidth * mbHeight
	mbPerSecond := mbPerFrame * maxInt(fps, 1)

	switch {
	case mbPerFrame <= 3600 && mbPerSecond <= 108000:
		return "1f" // level 3.1
	case mbPerFrame <= 8192 && mbPerSecond <= 245760:
		return "28" // level 4.0
	case mbPerFrame <= 8704 && mbPerSecond <= 522240:
		return "2a" // level 4.2
	default:
		return "33" // level 5.1
	}
}

func maxInt(value, fallback int) int {
	if value > fallback {
		return value
	}
	return fallback
}
