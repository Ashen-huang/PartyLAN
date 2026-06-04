package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/relaycfg"
	"PartyLAN/internal/room"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

var ErrUnhandledSignal = errors.New("media signal is not for an active host session")

type Signaler interface {
	SendMediaSignal(peerID string, payload room.MediaSignalPayload) error
}

type ShareConfig struct {
	SessionID    string
	HostID       string
	Width        int
	Height       int
	FPS          int
	Peers        []room.PeerInfo
	AudioEnabled bool
}

type ManagerConfig struct {
	ICEServers []webrtc.ICEServer
	Health     []string
}

type Manager struct {
	mu                sync.Mutex
	selfID            string
	signaler          Signaler
	api               *webrtc.API
	ice               []webrtc.ICEServer
	health            []string
	peerDiagnostics   map[string]peerICEDiagnostic
	sessions          map[string]*peerSession
	shareConf         ShareConfig
	onKeyFrameRequest func(peerID string, reason string)
	onDiagnostics     func()
	h264SampleCount   int
	h264KeyFrameCount int
}

type peerSession struct {
	peerID     string
	pc         *webrtc.PeerConnection
	videoTrack *webrtc.TrackLocalStaticSample
	audioTrack *webrtc.TrackLocalStaticSample
}

type peerICEDiagnostic struct {
	State    string
	Path     string
	Protocol string
	RelayVia string
}

func NewManager(selfID string, signaler Signaler, iceServers ...webrtc.ICEServer) *Manager {
	return NewManagerWithConfig(selfID, signaler, ManagerConfig{
		ICEServers: iceServers,
	})
}

func NewManagerWithConfig(selfID string, signaler Signaler, cfg ManagerConfig) *Manager {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		log.Printf("[media/webrtc] register default codecs failed: %v", err)
	}

	health := append([]string(nil), cfg.Health...)
	if len(health) == 0 {
		health = buildICEConfigHealth(cfg.ICEServers)
	}

	log.Printf("[media/webrtc] manager initialized self=%s ice_servers=%d", selfID, len(cfg.ICEServers))
	for _, line := range health {
		log.Printf("[media/ice] %s", line)
	}

	return &Manager{
		selfID:          selfID,
		signaler:        signaler,
		api:             webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine)),
		ice:             append([]webrtc.ICEServer(nil), cfg.ICEServers...),
		health:          health,
		peerDiagnostics: make(map[string]peerICEDiagnostic),
		sessions:        make(map[string]*peerSession),
	}
}

func ManagerConfigFromRelayConfig(cfg relaycfg.RuntimeConfig) ManagerConfig {
	return ManagerConfig{
		ICEServers: ICEServersFromRelayConfig(cfg),
		Health:     cfg.MediaHealthLines(),
	}
}

func ICEServersFromRelayConfig(cfg relaycfg.RuntimeConfig) []webrtc.ICEServer {
	servers := make([]webrtc.ICEServer, 0, len(cfg.ICEServers))
	for _, server := range cfg.ICEServers {
		if len(server.URLs) == 0 {
			continue
		}
		next := webrtc.ICEServer{
			URLs:       append([]string(nil), server.URLs...),
			Username:   server.Username,
			Credential: server.Credential,
		}
		servers = append(servers, next)
	}
	return servers
}

func ICEServersFromEnv() []webrtc.ICEServer {
	return ICEServersFromRelayConfig(relaycfg.Resolve(nil))
}

func (m *Manager) SetKeyFrameRequestHandler(fn func(peerID string, reason string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onKeyFrameRequest = fn
}

func (m *Manager) SetDiagnosticsChangeHandler(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDiagnostics = fn
}

func (m *Manager) Diagnostics() []string {
	m.mu.Lock()
	health := append([]string(nil), m.health...)
	peerDiagnostics := make(map[string]peerICEDiagnostic, len(m.peerDiagnostics))
	for peerID, diag := range m.peerDiagnostics {
		peerDiagnostics[peerID] = diag
	}
	m.mu.Unlock()

	peerIDs := make([]string, 0, len(peerDiagnostics))
	for peerID := range peerDiagnostics {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		diag := peerDiagnostics[peerID]
		if diag.Path != "" {
			line := fmt.Sprintf("peer=%s selected path=%s", peerID, diag.Path)
			if diag.Protocol != "" {
				line += " protocol=" + diag.Protocol
			}
			if diag.RelayVia != "" {
				line += " via=" + diag.RelayVia
			}
			health = append(health, line)
			continue
		}
		if diag.State != "" {
			health = append(health, fmt.Sprintf("peer=%s state=%s", peerID, diag.State))
		}
	}

	return health
}

func (m *Manager) requestKeyFrame(peerID string, reason string) {
	m.mu.Lock()
	fn := m.onKeyFrameRequest
	m.mu.Unlock()
	if fn == nil {
		return
	}
	fn(peerID, reason)
}

func (m *Manager) readSenderRTCP(peerID string, media string, sender *webrtc.RTPSender) {
	if sender == nil {
		return
	}
	go func() {
		for {
			packets, _, err := sender.ReadRTCP()
			if err != nil {
				if !errors.Is(err, io.ErrClosedPipe) {
					log.Printf("[media/webrtc] rtcp read stopped peer=%s media=%s: %v", peerID, media, err)
				}
				return
			}
			for _, packet := range packets {
				switch packet.(type) {
				case *rtcp.PictureLossIndication:
					log.Printf("[media/webrtc] rtcp keyframe request peer=%s media=%s type=pli", peerID, media)
					m.requestKeyFrame(peerID, "rtcp-pli")
				case *rtcp.FullIntraRequest:
					log.Printf("[media/webrtc] rtcp keyframe request peer=%s media=%s type=fir", peerID, media)
					m.requestKeyFrame(peerID, "rtcp-fir")
				}
			}
		}
	}()
}

func summarizeH264SDP(sdp string) string {
	h264PayloadTypes := make(map[string]bool)
	lines := strings.Split(strings.ReplaceAll(sdp, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "a=rtpmap:") || !strings.Contains(strings.ToUpper(line), "H264/") {
			continue
		}
		payloadType := sdpPayloadType(line)
		if payloadType != "" {
			h264PayloadTypes[payloadType] = true
		}
	}
	if len(h264PayloadTypes) == 0 {
		return "h264=none"
	}

	fmtpLines := make([]string, 0, len(h264PayloadTypes))
	for _, line := range lines {
		if !strings.HasPrefix(line, "a=fmtp:") {
			continue
		}
		if h264PayloadTypes[sdpPayloadType(line)] {
			fmtpLines = append(fmtpLines, line)
		}
	}
	if len(fmtpLines) == 0 {
		return "h264=fmtp:none"
	}
	return strings.Join(fmtpLines, " | ")
}

func sdpPayloadType(line string) string {
	_, afterColon, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	payloadType, _, _ := strings.Cut(afterColon, " ")
	return strings.TrimSpace(payloadType)
}

func (m *Manager) StartShare(ctx context.Context, cfg ShareConfig) error {
	if cfg.SessionID == "" {
		return fmt.Errorf("media share session id is required")
	}
	if cfg.HostID == "" {
		cfg.HostID = m.selfID
	}

	m.mu.Lock()
	m.shareConf = cfg
	m.h264SampleCount = 0
	m.h264KeyFrameCount = 0
	m.mu.Unlock()

	for _, peer := range cfg.Peers {
		if peer.ID == "" || peer.ID == m.selfID {
			continue
		}
		if err := m.AttachViewer(ctx, peer); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) AttachViewer(ctx context.Context, peer room.PeerInfo) error {
	if peer.ID == "" || peer.ID == m.selfID {
		return nil
	}

	m.mu.Lock()
	if _, exists := m.sessions[peer.ID]; exists {
		m.mu.Unlock()
		return nil
	}
	cfg := m.shareConf
	m.mu.Unlock()

	if cfg.SessionID == "" {
		return fmt.Errorf("media share is not active")
	}

	session, err := m.createPeerSession(ctx, peer.ID, cfg)
	if err != nil {
		return fmt.Errorf("create WebRTC session for %s: %w", peer.ID, err)
	}

	m.mu.Lock()
	m.sessions[peer.ID] = session
	m.mu.Unlock()
	return nil
}

func (m *Manager) StopShare() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*peerSession)
	m.shareConf = ShareConfig{}
	m.peerDiagnostics = make(map[string]peerICEDiagnostic)
	m.mu.Unlock()

	for _, session := range sessions {
		_ = session.pc.Close()
	}
	m.notifyDiagnosticsChange()
}

func (m *Manager) DetachViewer(peerID string) {
	m.mu.Lock()
	session := m.sessions[peerID]
	delete(m.sessions, peerID)
	delete(m.peerDiagnostics, peerID)
	m.mu.Unlock()
	if session != nil {
		_ = session.pc.Close()
	}
	m.notifyDiagnosticsChange()
}

func (m *Manager) SessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) WriteH264Sample(data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}

	info := inspectH264Sample(data)
	m.mu.Lock()
	m.h264SampleCount++
	sampleCount := m.h264SampleCount
	keyFrameCount := m.h264KeyFrameCount
	if info.HasIDR {
		m.h264KeyFrameCount++
		keyFrameCount = m.h264KeyFrameCount
	}
	sessions := make([]*peerSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	if sampleCount <= 3 || info.HasIDR || !info.AnnexB {
		log.Printf("[media/webrtc] h264 sample idx=%d key_idx=%d bytes=%d annexb=%t idr=%t sps=%t pps=%t profile_level_id=%s nal=%s",
			sampleCount,
			keyFrameCount,
			len(data),
			info.AnnexB,
			info.HasIDR,
			info.HasSPS,
			info.HasPPS,
			info.ProfileLevelID,
			describeH264NALTypes(info.NALTypes),
		)
	}

	var writeErr error
	for _, session := range sessions {
		if err := session.videoTrack.WriteSample(pionmedia.Sample{
			Data:     data,
			Duration: duration,
		}); err != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("peer %s: %w", session.peerID, err))
		}
	}
	return writeErr
}

func (m *Manager) WriteOpusSample(data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}

	m.mu.Lock()
	sessions := make([]*peerSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		if session.audioTrack != nil {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()

	var writeErr error
	for _, session := range sessions {
		if err := session.audioTrack.WriteSample(pionmedia.Sample{
			Data:     data,
			Duration: duration,
		}); err != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("peer %s audio: %w", session.peerID, err))
		}
	}
	return writeErr
}

func (m *Manager) HandleSignal(signal room.MediaSignalPayload) error {
	m.mu.Lock()
	session := m.sessions[signal.FromPeerID]
	m.mu.Unlock()
	if session == nil {
		return ErrUnhandledSignal
	}

	switch signal.Kind {
	case "answer":
		var answer webrtc.SessionDescription
		if err := json.Unmarshal(signal.Payload, &answer); err != nil {
			return fmt.Errorf("decode WebRTC answer: %w", err)
		}
		if err := session.pc.SetRemoteDescription(answer); err != nil {
			return fmt.Errorf("set remote answer: %w", err)
		}
		log.Printf("[media/webrtc] answer applied peer=%s session=%s", signal.FromPeerID, signal.SessionID)
		log.Printf("[media/webrtc] answer h264 peer=%s %s", signal.FromPeerID, summarizeH264SDP(answer.SDP))
		m.requestKeyFrame(signal.FromPeerID, "answer")
		return nil

	case "ice":
		var candidate webrtc.ICECandidateInit
		if err := json.Unmarshal(signal.Payload, &candidate); err != nil {
			return fmt.Errorf("decode ICE candidate: %w", err)
		}
		if candidate.Candidate == "" {
			return nil
		}
		if err := session.pc.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("add ICE candidate: %w", err)
		}
		return nil

	case "close":
		m.mu.Lock()
		delete(m.sessions, signal.FromPeerID)
		delete(m.peerDiagnostics, signal.FromPeerID)
		m.mu.Unlock()
		m.notifyDiagnosticsChange()
		return session.pc.Close()
	}

	return ErrUnhandledSignal
}

func (m *Manager) createPeerSession(ctx context.Context, peerID string, cfg ShareConfig) (*peerSession, error) {
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: m.ice,
	})
	if err != nil {
		return nil, err
	}

	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "screen", cfg.SessionID, webrtc.WithPayloader(func(_ webrtc.RTPCodecCapability) (rtp.Payloader, error) {
		return &codecs.H264Payloader{DisableStapA: true}, nil
	}))
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	videoSender, err := pc.AddTrack(track)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	m.readSenderRTCP(peerID, "video", videoSender)

	var audioTrack *webrtc.TrackLocalStaticSample
	if cfg.AudioEnabled {
		audioTrack, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		}, "share-app-audio", cfg.SessionID)
		if err != nil {
			_ = pc.Close()
			return nil, err
		}
		audioSender, err := pc.AddTrack(audioTrack)
		if err != nil {
			_ = pc.Close()
			return nil, err
		}
		m.readSenderRTCP(peerID, "audio", audioSender)
	}

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		payload, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			log.Printf("[media/webrtc] marshal ICE candidate peer=%s: %v", peerID, err)
			return
		}
		signal := room.MediaSignalPayload{
			SessionID:  cfg.SessionID,
			FromPeerID: m.selfID,
			ToPeerID:   peerID,
			Kind:       "ice",
			Media:      "screen",
			Payload:    payload,
		}
		if err := m.signaler.SendMediaSignal(peerID, signal); err != nil {
			log.Printf("[media/webrtc] send ICE candidate peer=%s: %v", peerID, err)
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[media/webrtc] peer=%s connection_state=%s", peerID, state.String())
		if state == webrtc.PeerConnectionStateConnected {
			m.requestKeyFrame(peerID, "connection-connected")
		}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[media/webrtc] peer=%s ice_state=%s", peerID, state.String())
		log.Printf("[media/ice] peer=%s state=%s", peerID, strings.ToLower(state.String()))
		m.updatePeerICEState(peerID, strings.ToLower(state.String()))
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			m.captureSelectedCandidatePair(peerID, pc)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		return nil, err
	}
	payload, err := json.Marshal(offer)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	log.Printf("[media/webrtc] offer h264 peer=%s %s", peerID, summarizeH264SDP(offer.SDP))
	signal := room.MediaSignalPayload{
		SessionID:  cfg.SessionID,
		FromPeerID: m.selfID,
		ToPeerID:   peerID,
		Kind:       "offer",
		Media:      "screen",
		Payload:    payload,
	}
	if err := m.signaler.SendMediaSignal(peerID, signal); err != nil {
		_ = pc.Close()
		return nil, err
	}
	log.Printf("[media/webrtc] offer sent peer=%s session=%s target=%dx%d@%dfps", peerID, cfg.SessionID, cfg.Width, cfg.Height, cfg.FPS)

	select {
	case <-ctx.Done():
		_ = pc.Close()
		return nil, ctx.Err()
	default:
	}

	return &peerSession{
		peerID:     peerID,
		pc:         pc,
		videoTrack: track,
		audioTrack: audioTrack,
	}, nil
}

func (m *Manager) updatePeerICEState(peerID string, state string) {
	m.mu.Lock()
	diag := m.peerDiagnostics[peerID]
	diag.State = state
	m.peerDiagnostics[peerID] = diag
	m.mu.Unlock()
	m.notifyDiagnosticsChange()
}

func (m *Manager) captureSelectedCandidatePair(peerID string, pc *webrtc.PeerConnection) {
	if pc == nil {
		return
	}

	path, protocol, relayVia, ok := selectedICEPath(pc.GetStats())
	if !ok {
		return
	}

	line := fmt.Sprintf("[media/ice] peer=%s selected path=%s", peerID, path)
	if protocol != "" {
		line += " protocol=" + protocol
	}
	if relayVia != "" {
		line += " via=" + relayVia
	}
	log.Print(line)

	m.mu.Lock()
	diag := m.peerDiagnostics[peerID]
	diag.Path = path
	diag.Protocol = protocol
	diag.RelayVia = relayVia
	m.peerDiagnostics[peerID] = diag
	m.mu.Unlock()
	m.notifyDiagnosticsChange()
}

func (m *Manager) notifyDiagnosticsChange() {
	m.mu.Lock()
	fn := m.onDiagnostics
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func buildICEConfigHealth(servers []webrtc.ICEServer) []string {
	if len(servers) == 0 {
		return []string{
			"media ICE servers not configured",
			"media TURN not configured",
		}
	}

	urlCount := 0
	hasTURN := false
	hasTURNCredentials := false
	for _, server := range servers {
		urlCount += len(server.URLs)
		for _, rawURL := range server.URLs {
			if isTURNURL(rawURL) {
				hasTURN = true
				if strings.TrimSpace(server.Username) != "" || credentialConfigured(server.Credential) {
					hasTURNCredentials = true
				}
			}
		}
	}

	lines := []string{
		fmt.Sprintf("media ICE servers configured: groups=%d urls=%d turn=%t", len(servers), urlCount, hasTURN),
	}
	switch {
	case hasTURN && hasTURNCredentials:
		lines = append(lines, "media TURN credentials configured")
	case hasTURN:
		lines = append(lines, "media TURN configured without credentials")
	default:
		lines = append(lines, "media TURN not configured")
	}

	return lines
}

func selectedICEPath(report webrtc.StatsReport) (path string, protocol string, relayVia string, ok bool) {
	candidates := make(map[string]webrtc.ICECandidateStats)
	var selectedPair *webrtc.ICECandidatePairStats

	for _, stat := range report {
		switch value := stat.(type) {
		case webrtc.ICECandidateStats:
			candidates[value.ID] = value
		case webrtc.ICECandidatePairStats:
			if !betterICECandidatePair(selectedPair, value) {
				continue
			}
			pairCopy := value
			selectedPair = &pairCopy
		}
	}
	if selectedPair == nil {
		return "", "", "", false
	}

	local, localOK := candidates[selectedPair.LocalCandidateID]
	remote, remoteOK := candidates[selectedPair.RemoteCandidateID]
	path = inferICEPath(local, localOK, remote, remoteOK)
	protocol = firstNonEmptyString(local.Protocol, remote.Protocol)
	relayVia = summarizeRelayVia(local, localOK, remote, remoteOK)
	return path, protocol, relayVia, true
}

func betterICECandidatePair(current *webrtc.ICECandidatePairStats, candidate webrtc.ICECandidatePairStats) bool {
	if current == nil {
		return true
	}
	if current.Nominated != candidate.Nominated {
		return candidate.Nominated
	}
	if current.State != candidate.State {
		return candidate.State == webrtc.StatsICECandidatePairStateSucceeded
	}
	currentBytes := current.BytesSent + current.BytesReceived
	candidateBytes := candidate.BytesSent + candidate.BytesReceived
	return candidateBytes > currentBytes
}

func inferICEPath(local webrtc.ICECandidateStats, localOK bool, remote webrtc.ICECandidateStats, remoteOK bool) string {
	if isRelayCandidate(local, localOK) || isRelayCandidate(remote, remoteOK) {
		return "relay"
	}
	if isReflexiveCandidate(local, localOK) || isReflexiveCandidate(remote, remoteOK) {
		return "srflx"
	}
	if localOK || remoteOK {
		return "host"
	}
	return "unknown"
}

func isRelayCandidate(candidate webrtc.ICECandidateStats, ok bool) bool {
	return ok && candidate.CandidateType == webrtc.ICECandidateTypeRelay
}

func isReflexiveCandidate(candidate webrtc.ICECandidateStats, ok bool) bool {
	return ok && (candidate.CandidateType == webrtc.ICECandidateTypeSrflx || candidate.CandidateType == webrtc.ICECandidateTypePrflx)
}

func summarizeRelayVia(local webrtc.ICECandidateStats, localOK bool, remote webrtc.ICECandidateStats, remoteOK bool) string {
	if via := candidateRelayURL(local, localOK); via != "" {
		return via
	}
	if via := candidateRelayURL(remote, remoteOK); via != "" {
		return via
	}
	return ""
}

func candidateRelayURL(candidate webrtc.ICECandidateStats, ok bool) string {
	if !ok || !isRelayCandidate(candidate, ok) || strings.TrimSpace(candidate.URL) == "" {
		return ""
	}
	parsed, err := url.Parse(candidate.URL)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return candidate.URL
	}
	return parsed.Host
}

func isTURNURL(rawURL string) bool {
	rawURL = strings.TrimSpace(strings.ToLower(rawURL))
	return strings.HasPrefix(rawURL, "turn:") || strings.HasPrefix(rawURL, "turns:")
}

func credentialConfigured(value any) bool {
	switch credential := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(credential) != ""
	default:
		return true
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
