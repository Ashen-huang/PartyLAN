package screegop2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/gamecast/adaptive"
	"PartyLAN/internal/gamecast/nativehost"
	gamecastws "PartyLAN/internal/gamecast/ws"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

type Signaler interface {
	Send(context.Context, OutgoingMessage) error
}

type PublisherConfig struct {
	Signaler              Signaler
	Width                 int
	Height                int
	FPS                   int
	AudioEnabled          bool
	PeerConnectionFactory func(webrtc.Configuration) (*webrtc.PeerConnection, error)
	OnKeyFrameRequest     func(reason string)
	OnStatus              func(gamecastws.Status)
}

type ViewerTransportSnapshot struct {
	SessionID            string
	PeerID               string
	LayerID              string
	EstimatedBitrateKbps int
	EstimateValid        bool
	ConnectionState      string
}

type Publisher struct {
	config PublisherConfig

	mu       sync.Mutex
	ctx      context.Context
	started  bool
	sessions map[string]*publisherSession
}

type publisherSession struct {
	mu         sync.Mutex
	id         string
	peer       string
	pc         *webrtc.PeerConnection
	video      *webrtc.TrackLocalStaticSample
	audio      *webrtc.TrackLocalStaticSample
	layerID    string
	bwe        cc.BandwidthEstimator
	pendingICE []webrtc.ICECandidateInit
}

func NewPublisher(config PublisherConfig) *Publisher {
	return &Publisher{
		config:   config,
		sessions: make(map[string]*publisherSession),
	}
}

func (p *Publisher) Start(ctx context.Context) error {
	if p.config.Signaler == nil {
		return fmt.Errorf("Screego P2P publisher requires a signaler")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.ctx = ctx
	p.started = true
	p.mu.Unlock()
	if err := p.send(OutgoingMessage{Type: MessageShare, Payload: struct{}{}}); err != nil {
		p.mu.Lock()
		p.started = false
		p.mu.Unlock()
		return err
	}
	p.emit(gamecastws.Status{State: "connected", Detail: "native P2P host sharing"})
	return nil
}

func (p *Publisher) Resume() error {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil
	}
	return p.send(OutgoingMessage{Type: MessageShare, Payload: struct{}{}})
}

func (p *Publisher) HandleMessage(message IncomingMessage) error {
	switch message.Type {
	case MessageHostSession:
		session, err := message.Session()
		if err != nil {
			return err
		}
		return p.startSession(session)
	case MessageClientAnswer, MessageClientICE:
		signal, err := message.P2P()
		if err != nil {
			return err
		}
		p.mu.Lock()
		session := p.sessions[signal.SID]
		p.mu.Unlock()
		if session == nil {
			return nil
		}
		if message.Type == MessageClientAnswer {
			return p.acceptAnswer(session, signal.Value)
		}
		return p.addICECandidate(session, signal.Value)
	case MessageICERestart:
		var request ICERecoveryPayload
		if err := json.Unmarshal(message.Payload, &request); err != nil || strings.TrimSpace(request.SID) == "" {
			return &ProtocolError{Code: ErrorProtocol, detail: "invalid ICE recovery request"}
		}
		if request.Action != "restart" {
			return nil
		}
		p.mu.Lock()
		session := p.sessions[request.SID]
		p.mu.Unlock()
		if session == nil {
			return nil
		}
		return p.restartICE(session)
	case MessageEndShare:
		var sid string
		if err := json.Unmarshal(message.Payload, &sid); err != nil {
			return &ProtocolError{Code: ErrorProtocol, detail: "invalid endshare payload"}
		}
		if strings.TrimSpace(sid) == "" {
			return nil
		}
		p.closeSession(sid)
	}
	return nil
}

func (p *Publisher) Close() error {
	p.mu.Lock()
	started := p.started
	p.started = false
	sessions := make([]*publisherSession, 0, len(p.sessions))
	for _, session := range p.sessions {
		sessions = append(sessions, session)
	}
	p.sessions = make(map[string]*publisherSession)
	p.mu.Unlock()

	var firstErr error
	if started {
		if err := p.send(OutgoingMessage{Type: MessageStopShare, Payload: struct{}{}}); err != nil {
			firstErr = err
		}
	}
	for _, session := range sessions {
		if err := session.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.emit(gamecastws.Status{State: "closed", Detail: "native P2P sharing stopped"})
	return firstErr
}

func (p *Publisher) SessionCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions)
}

func (p *Publisher) PendingICECount(sid string) int {
	p.mu.Lock()
	session := p.sessions[strings.TrimSpace(sid)]
	p.mu.Unlock()
	if session == nil {
		return 0
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.pendingICE)
}

func (p *Publisher) ActiveVideoLayers() []nativehost.VideoLayer {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil
	}
	return nativehost.SingleVideoLayer(p.config.Width, p.config.Height, p.config.FPS)
}

func (p *Publisher) WriteH264Sample(data []byte, duration time.Duration) error {
	return p.WriteH264LayerSample("0", data, duration)
}

func (p *Publisher) WriteH264LayerSample(rid string, data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}
	p.mu.Lock()
	sessions := make([]*publisherSession, 0, len(p.sessions))
	for _, session := range p.sessions {
		sessions = append(sessions, session)
	}
	p.mu.Unlock()
	var firstErr error
	for _, session := range sessions {
		if err := session.writeVideo(rid, data, duration); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *Publisher) SetViewerLayer(sessionID, layerID string) error {
	sessionID = strings.TrimSpace(sessionID)
	layerID = normalizeLayerID(layerID)
	p.mu.Lock()
	session := p.sessions[sessionID]
	p.mu.Unlock()
	if session == nil {
		return fmt.Errorf("unknown P2P session %q", sessionID)
	}
	session.mu.Lock()
	session.layerID = layerID
	session.mu.Unlock()
	return nil
}

func (p *Publisher) SwitchViewerLayer(sessionID, layerID string, idr []byte, duration time.Duration) error {
	sessionID = strings.TrimSpace(sessionID)
	layerID = normalizeLayerID(layerID)
	if len(idr) == 0 {
		return fmt.Errorf("layer %q switch requires an IDR sample", layerID)
	}
	p.mu.Lock()
	session := p.sessions[sessionID]
	p.mu.Unlock()
	if session == nil {
		return fmt.Errorf("unknown P2P session %q", sessionID)
	}
	return session.switchLayer(layerID, idr, duration)
}

func (p *Publisher) ViewerTransportSnapshots() []ViewerTransportSnapshot {
	p.mu.Lock()
	sessions := make([]*publisherSession, 0, len(p.sessions))
	for _, session := range p.sessions {
		sessions = append(sessions, session)
	}
	p.mu.Unlock()
	snapshots := make([]ViewerTransportSnapshot, 0, len(sessions))
	for _, session := range sessions {
		session.mu.Lock()
		snapshot := ViewerTransportSnapshot{
			SessionID: session.id, PeerID: session.peer, LayerID: session.layerID,
		}
		if session.pc != nil {
			snapshot.ConnectionState = session.pc.ConnectionState().String()
		}
		if session.bwe != nil {
			snapshot.EstimatedBitrateKbps = session.bwe.GetTargetBitrate() / 1000
			snapshot.EstimateValid = snapshot.EstimatedBitrateKbps > 0
		}
		session.mu.Unlock()
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func (p *Publisher) WriteOpusSample(data []byte, duration time.Duration) error {
	if len(data) == 0 {
		return nil
	}
	p.mu.Lock()
	sessions := make([]*publisherSession, 0, len(p.sessions))
	for _, session := range p.sessions {
		sessions = append(sessions, session)
	}
	p.mu.Unlock()
	var firstErr error
	for _, session := range sessions {
		if err := session.writeAudio(data, duration); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *Publisher) startSession(payload SessionPayload) error {
	p.mu.Lock()
	started := p.started
	old := p.sessions[payload.ID]
	p.mu.Unlock()
	if !started {
		return nil
	}
	if old != nil {
		p.closeSession(payload.ID)
	}
	pc, videoTrack, audioTrack, bwe, err := p.createPeerConnection(payload)
	if err != nil {
		return err
	}
	session := &publisherSession{id: payload.ID, peer: payload.Peer, pc: pc, video: videoTrack, audio: audioTrack, layerID: "0", bwe: bwe}
	p.mu.Lock()
	p.sessions[payload.ID] = session
	p.mu.Unlock()

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		p.closeSession(payload.ID)
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		p.closeSession(payload.ID)
		return err
	}
	return p.send(OutgoingMessage{Type: MessageHostOffer, Payload: P2PPayload{SID: payload.ID, Value: mustJSON(offer)}})
}

func (p *Publisher) createPeerConnection(payload SessionPayload) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticSample, *webrtc.TrackLocalStaticSample, cc.BandwidthEstimator, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, nil, nil, nil, err
	}
	configuration := webrtc.Configuration{ICEServers: make([]webrtc.ICEServer, 0, len(payload.ICEServers))}
	for _, server := range payload.ICEServers {
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	factory := p.config.PeerConnectionFactory
	var bwe cc.BandwidthEstimator
	var pc *webrtc.PeerConnection
	var err error
	if factory == nil {
		pc, bwe, err = newPeerConnectionWithBWE(configuration, mediaEngine, p.config.Width, p.config.Height, p.config.FPS)
		if err != nil {
			log.Printf("[gamecast/screego] P2P GCC unavailable, using default interceptors: %v", err)
			fallbackMediaEngine := &webrtc.MediaEngine{}
			if codecErr := fallbackMediaEngine.RegisterDefaultCodecs(); codecErr != nil {
				return nil, nil, nil, nil, codecErr
			}
			pc, err = webrtc.NewAPI(webrtc.WithMediaEngine(fallbackMediaEngine)).NewPeerConnection(configuration)
			bwe = nil
		}
	} else {
		pc, err = factory(configuration)
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	layers := nativehost.SingleVideoLayer(p.config.Width, p.config.Height, p.config.FPS)
	videoTrack, err := newH264Track(layers[0])
	if err != nil {
		_ = pc.Close()
		return nil, nil, nil, nil, err
	}
	var audioTrack *webrtc.TrackLocalStaticSample
	if p.config.AudioEnabled {
		audioTrack, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		}, "share-app-audio", "screego-p2p-audio")
		if err != nil {
			_ = pc.Close()
			return nil, nil, nil, nil, err
		}
		audioSender, err := pc.AddTrack(audioTrack)
		if err != nil {
			_ = pc.Close()
			return nil, nil, nil, nil, err
		}
		go drainSenderRTCP(audioSender)
	}
	sessionID := payload.ID
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		if err := p.send(OutgoingMessage{Type: MessageHostICE, Payload: P2PPayload{SID: sessionID, Value: mustJSON(candidate.ToJSON())}}); err != nil {
			log.Printf("[gamecast/screego] host ICE send failed session=%s: %v", sessionID, err)
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		switch state {
		case webrtc.PeerConnectionStateConnected:
			p.emit(gamecastws.Status{State: "connected", Detail: "native P2P viewer connected"})
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			p.closeSession(sessionID)
		}
	})
	if sender, err := pc.AddTrack(videoTrack); err == nil {
		go p.readSenderRTCP(sessionID, sender)
	} else {
		_ = pc.Close()
		return nil, nil, nil, nil, err
	}
	return pc, videoTrack, audioTrack, bwe, nil
}

func newPeerConnectionWithBWE(configuration webrtc.Configuration, mediaEngine *webrtc.MediaEngine, width, height, fps int) (*webrtc.PeerConnection, cc.BandwidthEstimator, error) {
	registry := &interceptor.Registry{}
	estimatorCh := make(chan cc.BandwidthEstimator, 1)
	ladder := adaptive.BuildLadder(adaptive.Profile{Width: width, Height: height, FPS: fps, BitrateKbps: 50000})
	initialBitrateKbps := ladder[0].BitrateKbps
	ccFactory, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
		return gcc.NewSendSideBWE(
			gcc.SendSideBWEInitialBitrate(initialBitrateKbps*1000),
			gcc.SendSideBWEMaxBitrate(publisherMaxBitrateBps(initialBitrateKbps)),
		)
	})
	if err != nil {
		return nil, nil, err
	}
	ccFactory.OnNewPeerConnection(func(_ string, estimator cc.BandwidthEstimator) {
		select {
		case estimatorCh <- estimator:
		default:
		}
	})
	registry.Add(ccFactory)
	if err := webrtc.ConfigureTWCCHeaderExtensionSender(mediaEngine, registry); err != nil {
		return nil, nil, err
	}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, nil, err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry))
	pc, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, nil, err
	}
	select {
	case estimator := <-estimatorCh:
		return pc, estimator, nil
	default:
		_ = pc.Close()
		return nil, nil, fmt.Errorf("P2P GCC estimator was not created")
	}
}

func publisherMaxBitrateBps(initialBitrateKbps int) int {
	const approvedBurstCeilingBps = 50_000_000
	initialBitrateBps := initialBitrateKbps * 1000
	if initialBitrateBps > approvedBurstCeilingBps {
		return initialBitrateBps
	}
	return approvedBurstCeilingBps
}

func (p *Publisher) readSenderRTCP(sessionID string, sender *webrtc.RTPSender) {
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
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				if p.config.OnKeyFrameRequest != nil {
					p.config.OnKeyFrameRequest("screego-p2p-" + sessionID)
				}
			}
		}
	}
}

func drainSenderRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

func (p *Publisher) acceptAnswer(session *publisherSession, raw json.RawMessage) error {
	var answer webrtc.SessionDescription
	if err := json.Unmarshal(raw, &answer); err != nil {
		return err
	}
	if answer.Type == webrtc.SDPTypeUnknown {
		answer.Type = webrtc.SDPTypeAnswer
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.pc.SetRemoteDescription(answer); err != nil {
		return err
	}
	pending := append([]webrtc.ICECandidateInit(nil), session.pendingICE...)
	session.pendingICE = nil
	for _, candidate := range pending {
		if err := session.pc.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (p *Publisher) addICECandidate(session *publisherSession, raw json.RawMessage) error {
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pc.RemoteDescription() == nil {
		session.pendingICE = append(session.pendingICE, candidate)
		return nil
	}
	return session.pc.AddICECandidate(candidate)
}

func (p *Publisher) restartICE(session *publisherSession) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	offer, err := session.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return err
	}
	if err := session.pc.SetLocalDescription(offer); err != nil {
		return err
	}
	return p.send(OutgoingMessage{Type: MessageHostOffer, Payload: P2PPayload{SID: session.id, Value: mustJSON(offer)}})
}

func (p *Publisher) closeSession(sid string) {
	p.mu.Lock()
	session := p.sessions[sid]
	delete(p.sessions, sid)
	p.mu.Unlock()
	if session != nil {
		_ = session.close()
	}
}

func (p *Publisher) send(message OutgoingMessage) error {
	p.mu.Lock()
	ctx := p.ctx
	signaler := p.config.Signaler
	p.mu.Unlock()
	if signaler == nil {
		return &ProtocolError{Code: ErrorUnavailable, detail: "Screego signaler is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return signaler.Send(sendCtx, message)
}

func (p *Publisher) emit(status gamecastws.Status) {
	if p.config.OnStatus != nil {
		p.config.OnStatus(status)
	}
}

func (s *publisherSession) close() error {
	s.mu.Lock()
	pc := s.pc
	bwe := s.bwe
	s.pc = nil
	s.video = nil
	s.audio = nil
	s.bwe = nil
	s.pendingICE = nil
	s.mu.Unlock()
	if pc != nil {
		return pc.Close()
	}
	if bwe != nil {
		return bwe.Close()
	}
	return nil
}

func (s *publisherSession) writeVideo(layerID string, data []byte, duration time.Duration) error {
	s.mu.Lock()
	track := s.video
	assignedLayer := s.layerID
	s.mu.Unlock()
	if track == nil || assignedLayer != normalizeLayerID(layerID) {
		return nil
	}
	return track.WriteSample(pionmedia.Sample{Data: data, Duration: duration})
}

func (s *publisherSession) switchLayer(layerID string, idr []byte, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.video == nil {
		return nil
	}
	if err := s.video.WriteSample(pionmedia.Sample{Data: idr, Duration: duration}); err != nil {
		return err
	}
	s.layerID = layerID
	return nil
}

func normalizeLayerID(layerID string) string {
	layerID = strings.TrimSpace(strings.ToLower(layerID))
	if layerID == "" || layerID == "f" {
		return "0"
	}
	return layerID
}

func (s *publisherSession) writeAudio(data []byte, duration time.Duration) error {
	s.mu.Lock()
	track := s.audio
	s.mu.Unlock()
	if track == nil {
		return nil
	}
	return track.WriteSample(pionmedia.Sample{Data: data, Duration: duration})
}

func newH264Track(layer nativehost.VideoLayer) (*webrtc.TrackLocalStaticSample, error) {
	return webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: h264SDPFmtpLine(layer.Width, layer.Height, layer.FPS),
	}, "screen", "screego-p2p-video", webrtc.WithPayloader(func(webrtc.RTPCodecCapability) (rtp.Payloader, error) {
		return &codecs.H264Payloader{DisableStapA: true}, nil
	}))
}

func h264SDPFmtpLine(width, height, fps int) string {
	return fmt.Sprintf("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e0%s", h264LevelIDHex(width, height, fps))
}

func h264LevelIDHex(width, height, fps int) string {
	mbWidth := (maxInt((width+15)/16, 1))
	mbHeight := (maxInt((height+15)/16, 1))
	mbPerFrame := mbWidth * mbHeight
	mbPerSecond := mbPerFrame * maxInt(fps, 1)
	switch {
	case mbPerFrame <= 3600 && mbPerSecond <= 108000:
		return "1f"
	case mbPerFrame <= 8192 && mbPerSecond <= 245760:
		return "28"
	case mbPerFrame <= 8704 && mbPerSecond <= 522240:
		return "2a"
	default:
		return "33"
	}
}

func maxInt(value, fallback int) int {
	if value > fallback {
		return value
	}
	return fallback
}

func mustJSON(value interface{}) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}
