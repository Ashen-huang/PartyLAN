package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"math"
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
	"PartyLAN/internal/gamecast"
	"PartyLAN/internal/gamecast/adaptive"
	hostwindowcapture "PartyLAN/internal/gamecast/host/windowcapture"
	"PartyLAN/internal/gamecast/nativehost"
	"PartyLAN/internal/gamecast/rooms"
	"PartyLAN/internal/gamecast/screegop2p"
	"PartyLAN/internal/gamecast/session"
	"PartyLAN/internal/gamecast/shareaudio"
	"PartyLAN/internal/gamecast/sharecatalog"
	gamecastvoice "PartyLAN/internal/gamecast/voice"
	gamecastws "PartyLAN/internal/gamecast/ws"
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

	gameCastStartupKeyFrameWindow   = 5 * time.Second
	gameCastStartupKeyFrameCooldown = 1500 * time.Millisecond
	gameCastSteadyKeyFrameCooldown  = 300 * time.Millisecond

	defaultNetworkStartTimeout = 5 * time.Second
	authKeyNetworkStartTimeout = 30 * time.Second

	defaultWindowWidth       = 1400
	defaultWindowHeight      = 810
	maxWindowWidthRatio      = 0.96
	maxWindowHeightRatio     = 0.92
	smallScreenWidthCutoff   = 1800
	smallScreenHeightCutoff  = 1100
	windowLayoutVersion      = 4
	maxDisplayNameRunes      = 40
	gameCastMediaStopTimeout = 2 * time.Second
	gameCastHostReadyTimeout = 10 * time.Second
)

type App struct {
	ctx                        context.Context
	network                    network.Network
	room                       *room.RoomImpl
	hostVideoCapability        video.BackendCapability
	audioCapability            windowsaudio.Capability
	cfg                        *config.Config
	relayConfig                relaycfg.RuntimeConfig
	audioShareSession          *audio.ShareSession
	voiceChannel               voice.VoiceChannel
	mediaManager               *media.Manager
	gamecastWS                 *gamecastws.Client
	gamecastWSGeneration       uint64
	gamecastP2PRoom            *screegop2p.Client
	gamecastP2PViewer          *screegop2p.Viewer
	gamecastP2PPublisher       *screegop2p.Publisher
	gamecastNativeHost         gameCastNativePublisher
	gamecastNativeShare        *shareSession
	gamecastJar                *session.Jar
	gamecastRoomCancel         context.CancelFunc
	gamecastPresenceCancel     context.CancelFunc
	gamecastPresenceRoom       string
	gamecastPresenceRole       string
	gamecastPresenceOrigin     string
	gamecastPresenceMediaMode  string
	gamecastParticipantID      string
	gamecastShareCancel        context.CancelFunc
	gamecastShareLease         *gamecast.ShareLease
	gamecastViewerIntentCancel context.CancelFunc
	gamecastViewerIntentRoom   string
	gamecastQualityFailureLog  time.Time
	gamecastVoice              roomVoiceSession
	gamecastVoiceFactory       func(gamecastvoice.Config) roomVoiceSession
	processLookup              func(int) (ps.Process, error)
	gamecastShareAudioResolver gameCastShareAudioResolver
	windowProcessResolver      func(uintptr) (sharecatalog.WindowProcess, error)
	gamecastAudioLoopStarter   func(gameCastNativePublisher, *shareSession, audioCaptureTarget) error
	gamecastMediaCancel        context.CancelFunc
	gamecastMediaDone          chan struct{}
	mediaCancel                context.CancelFunc
	mediaDone                  chan struct{}
	audioMediaCancel           context.CancelFunc
	audioMediaDone             chan struct{}
	mediaKeyFrameSeq           atomic.Int64
	gamecastRuntimeMu          sync.Mutex

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

type gameCastNativePublisher interface {
	Close() error
	WriteH264Sample(data []byte, duration time.Duration) error
	WriteH264LayerSample(rid string, data []byte, duration time.Duration) error
	WriteOpusSample(data []byte, duration time.Duration) error
	SessionCount() int
	ActiveVideoLayers() []nativehost.VideoLayer
}

func NewApp(net network.Network) *App {
	return &App{
		network:               net,
		senders:               make(map[string]*room.StreamSender),
		receivers:             make(map[string]*room.StreamReceiver),
		audioShareSelection:   make(map[string]bool),
		audioShareSession:     audio.NewShareSession(),
		voiceVolume:           0.8,
		gamecastParticipantID: rooms.GenerateRoomID("client"),
		gamecastVoiceFactory: func(config gamecastvoice.Config) roomVoiceSession {
			return gamecastvoice.New(config)
		},
		processLookup:              ps.FindProcess,
		gamecastShareAudioResolver: shareaudio.NewResolver(),
		windowProcessResolver:      sharecatalog.ResolveWindowProcess,
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

type GameCastProfile struct {
	BaseURL                  string `json:"baseUrl"`
	DisplayName              string `json:"displayName,omitempty"`
	TrustedCertificateSHA256 string `json:"trustedCertificateSha256,omitempty"`
}

type GameCastProbeResult = gamecast.ProbeResult
type GameCastRoomState = gamecast.RoomState
type GameCastStatus = gamecastws.Status
type GameCastPresenceParticipant = gamecast.PresenceParticipant

type GameCastSessionState struct {
	User     string `json:"user"`
	LoggedIn bool   `json:"loggedIn"`
	AuthMode string `json:"authMode,omitempty"`
	Version  string `json:"version,omitempty"`
	RoomName string `json:"roomName,omitempty"`
}

type GameCastCredentials struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type GameCastRoomSession struct {
	RoomID string            `json:"roomId"`
	Role   string            `json:"role"`
	State  GameCastRoomState `json:"state"`
}

type GameCastHostShareRequest struct {
	RoomID           string `json:"roomId"`
	Sharing          bool   `json:"sharing"`
	PeerID           string `json:"peerId,omitempty"`
	SourceID         string `json:"sourceId,omitempty"`
	SourceKind       string `json:"sourceKind,omitempty"`
	SourceLabel      string `json:"sourceLabel,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	FPS              int    `json:"fps,omitempty"`
	AudioMode        string `json:"audioMode,omitempty"`
	AudioProcessName string `json:"audioProcessName,omitempty"`
	AudioProcessID   uint32 `json:"audioProcessId,omitempty"`
}

type audioCaptureTarget struct {
	PID                 uint32
	Name                string
	Mode                windowsaudio.CaptureMode
	ApplicationIdentity string
	IdentityStable      bool
	Reason              string
}

type gameCastShareAudioResolver interface {
	ResolveWindow(shareaudio.WindowTarget) (shareaudio.Target, error)
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

type roomVoiceSession interface {
	Start(context.Context) error
	Stop()
	Snapshot() gamecastvoice.State
	SetMicrophoneEnabled(bool) gamecastvoice.State
	SetPlaybackMuted(bool) gamecastvoice.State
}

type shareSession struct {
	roomID               string
	peerID               string
	displayIndex         int
	windowHandle         uintptr
	sourceKind           string
	sourceLabel          string
	audioProcessID       uint32
	audioProcessName     string
	audioTarget          audioCaptureTarget
	audioRebindAttempted bool
	width                int
	height               int
	fps                  int
	audioMode            string
	ready                <-chan struct{}
}

func logVideoCapability(prefix string, capability video.BackendCapability) {
	log.Printf(
		"%s backend=%s status=%s supports_host=%t supports_view=%t adapter=%q driver=%q reason=%q",
		prefix,
		capability.Backend,
		capability.Status,
		capability.SupportsHost,
		capability.SupportsView,
		capability.Adapter,
		capability.Driver,
		capability.Reason,
	)
}

func logAudioCapability(prefix string, capability windowsaudio.Capability) {
	log.Printf(
		"%s supports_process_capture=%t reason=%q",
		prefix,
		capability.SupportsProcessCapture,
		capability.Reason,
	)
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
	logVideoCapability("[video/capability] startup", a.hostVideoCapability)
	logAudioCapability("[audio/capability] startup", a.audioCapability)

	cfg, err := config.Load()
	if err != nil {
		log.Printf("load config failed: %v", err)
		cfg = config.DefaultConfig()
	}
	if migrateGameCastConfig(cfg) {
		if err := config.Save(cfg); err != nil {
			log.Printf("migrate gamecast config failed: %v", err)
		}
	}
	a.cfg = cfg
	if err := a.ensureGameCastJar(gameCastProfileFromConfig(cfg)); err != nil {
		log.Printf("load gamecast session jar failed: %v", err)
	}
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
		a.emitError(app.ErrCodeNetworkStart, "网络启动失败，请检查网络环境、认证配置或当前构建模式。")
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

func migrateGameCastConfig(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	normalized, migrated := gamecast.MigrateLegacyBaseURL(cfg.GameCast.BaseURL)
	if !migrated {
		return false
	}
	cfg.GameCast.BaseURL = normalized
	cfg.GameCast.TrustedCertificateSHA256 = ""
	return true
}

func (a *App) domReady(ctx context.Context) {
	a.ctx = ctx
	a.applyInitialWindowSizing()
}

func (a *App) shutdown(ctx context.Context) {
	a.persistConfig()
	if err := a.stopGameCastScreenShare(); err != nil {
		log.Printf("[gamecast/shutdown] screen share cleanup failed: %v", err)
	}
	a.closeGameCastP2PRoom()
	a.stopGameCastPresence(true)
	a.stopGameCastRoomHeartbeat(true)
	if err := a.network.Stop(); err != nil {
		log.Printf("network stop failed: %v", err)
		a.emitError(app.ErrCodeNetworkStop, "网络关闭失败。")
	}
}

func (a *App) CreateRoom(name, password string) error {
	if a.room != nil {
		return fmt.Errorf("已经在房间中，无法重复创建。")
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
		return fmt.Errorf("已经在房间中，无法重复加入。")
	}
	targetNodeID = normalizeJoinTarget(targetNodeID)
	if targetNodeID == "" {
		return fmt.Errorf("请输入目标主机节点 ID。")
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
		logVideoCapability("[video/capability] webrtc-share-rejected", a.hostVideoCapability)
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
		return fmt.Errorf("当前已经在共享中。")
	}
	a.mu.Unlock()

	displays := video.ListDisplays()
	if displayIndex < 0 || displayIndex >= len(displays) {
		return fmt.Errorf("所选显示器无效。")
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
	sourceBounds := video.ListDisplays()[session.displayIndex]
	allowDirectNative := shouldUseDirectNativeEncode(sourceBounds.Dx(), sourceBounds.Dy(), session.width, session.height)
	if allowDirectNative {
		encoder.SetCapturerHandle(capturer.Handle())
	} else {
		encoder.SetCapturerHandle(0)
		log.Printf(
			"[media/webrtc] direct native encode bypassed source=%dx%d target=%dx%d reason=%q",
			sourceBounds.Dx(),
			sourceBounds.Dy(),
			session.width,
			session.height,
			"target resolution requires resize",
		)
	}
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

	go a.runWebRTCVideoLoop(loopCtx, manager, a.room.SelfID(), capturer, encoder, session.width, session.height, session.fps, allowDirectNative, done)
	return nil
}

type gameCastVideoEncoder struct {
	layer   nativehost.VideoLayer
	encoder *video.NativeEncoder
}

type gameCastKeyFrameGate struct {
	startedAt time.Time
	lastAt    time.Time
}

func newGameCastKeyFrameGate(startedAt time.Time) *gameCastKeyFrameGate {
	return &gameCastKeyFrameGate{startedAt: startedAt}
}

func (g *gameCastKeyFrameGate) Allow(now time.Time) bool {
	if g == nil {
		return true
	}
	if g.startedAt.IsZero() {
		g.startedAt = now
	}
	cooldown := gameCastSteadyKeyFrameCooldown
	if now.Sub(g.startedAt) < gameCastStartupKeyFrameWindow {
		cooldown = gameCastStartupKeyFrameCooldown
	}
	if !g.lastAt.IsZero() && now.Sub(g.lastAt) < cooldown {
		return false
	}
	g.lastAt = now
	return true
}

func chooseGameCastVideoFrame(lastFrame, latestFrame *video.Frame) *video.Frame {
	if latestFrame != nil {
		return latestFrame
	}
	return lastFrame
}

func offerLatestGameCastVideoFrame(frames chan *video.Frame, frame *video.Frame) {
	if frame == nil {
		return
	}
	select {
	case frames <- frame:
		return
	default:
	}
	select {
	case <-frames:
	default:
	}
	select {
	case frames <- frame:
	default:
	}
}

func newGameCastVideoEncoders(layers []nativehost.VideoLayer) ([]gameCastVideoEncoder, error) {
	encoders := make([]gameCastVideoEncoder, 0, len(layers))
	for _, layer := range layers {
		encoder := video.NewNativeEncoder()
		encoder.SetInputPixelFormat(video.NativePixelFormatBGRA)
		if err := encoder.Configure(video.EncoderConfig{
			Codec:     video.CodecH264,
			Bitrate:   layer.BitrateKbps,
			Width:     layer.Width,
			Height:    layer.Height,
			FrameRate: layer.FPS,
		}); err != nil {
			_ = encoder.Close()
			closeGameCastVideoEncoders(encoders)
			return nil, fmt.Errorf("configure %s single-track encoder: %w", layer.RID, err)
		}
		encoders = append(encoders, gameCastVideoEncoder{layer: layer, encoder: encoder})
	}
	return encoders, nil
}

func closeGameCastVideoEncoders(encoders []gameCastVideoEncoder) {
	for _, entry := range encoders {
		if entry.encoder != nil {
			_ = entry.encoder.Close()
		}
	}
}

func (a *App) startGameCastNativeVideoLoop(host gameCastNativePublisher, session *shareSession) error {
	if host == nil || session == nil {
		return fmt.Errorf("GameCast native video loop requires an active host session")
	}

	a.stopGameCastMediaLoop()

	capturer, err := newGameCastResilientDisplayCapturer(session, func(usingBackup bool) {
		backend := "dxgi"
		message := "原生全屏采集已恢复。"
		if usingBackup {
			backend = "screenshot"
			message = "原生全屏采集异常，已切换备用采集。"
		}
		seq := a.mediaKeyFrameSeq.Add(1)
		log.Printf("[gamecast/capture-health] switched backend=%s keyframe_seq=%d", backend, seq)
		a.emitGameCastShareRuntime(app.GameCastShareRuntimeEvent{
			Kind: "capture-backend", RoomID: session.roomID, PeerID: session.peerID,
			AudioMode: session.audioMode, Message: message,
		})
	})
	if err != nil {
		return err
	}

	encoders, err := newGameCastVideoEncoders(nativehost.SingleVideoLayer(session.width, session.height, session.fps))
	if err != nil {
		_ = capturer.Stop()
		return err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	a.mu.Lock()
	a.gamecastMediaCancel = cancel
	a.gamecastMediaDone = done
	a.mu.Unlock()

	go a.runGameCastVideoLoop(
		loopCtx,
		host,
		capturer.GetFrame,
		capturer.Stop,
		encoders,
		session,
		"display-capture",
		shouldRetryGameCastDisplayCapture,
		func(err error) {
			log.Printf("[gamecast/display-capture] capture stopped: %v", err)
			a.handleGameCastNativeVideoFailure(session, err)
		},
		done,
	)
	return nil
}

func (a *App) startGameCastWindowVideoLoop(host gameCastNativePublisher, session *shareSession) error {
	if host == nil || session == nil || session.windowHandle == 0 {
		return fmt.Errorf("GameCast window video loop requires a selected window")
	}

	a.stopGameCastMediaLoop()

	capturer := hostwindowcapture.New(session.windowHandle, session.fps)
	if err := capturer.Start(); err != nil {
		return fmt.Errorf("start window capturer: %w", err)
	}

	encoders, err := newGameCastVideoEncoders(nativehost.SingleVideoLayer(session.width, session.height, session.fps))
	if err != nil {
		_ = capturer.Stop()
		return err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	a.mu.Lock()
	a.gamecastMediaCancel = cancel
	a.gamecastMediaDone = done
	a.mu.Unlock()

	go a.runGameCastNativeWindowVideoLoop(loopCtx, host, capturer, encoders, session, done)
	return nil
}

func (a *App) startGameCastVideoLoop(host gameCastNativePublisher, session *shareSession) error {
	if session == nil {
		return fmt.Errorf("GameCast video loop requires an active host session")
	}
	if session.sourceKind == string(sharecatalog.SourceKindWindow) {
		return a.startGameCastWindowVideoLoop(host, session)
	}
	return a.startGameCastNativeVideoLoop(host, session)
}

func (a *App) runGameCastVideoLoop(
	ctx context.Context,
	host gameCastNativePublisher,
	getFrame func() (*video.Frame, error),
	stopCapture func() error,
	encoders []gameCastVideoEncoder,
	session *shareSession,
	mode string,
	shouldRetry func(error) bool,
	onFatalCaptureError func(error),
	done chan<- struct{},
) {
	defer close(done)
	if len(encoders) == 0 || encoders[0].encoder == nil {
		log.Printf("[gamecast/%s] video loop has no encoder", mode)
		return
	}
	captureCtx, cancelCapture := context.WithCancel(ctx)
	latestFrames := make(chan *video.Frame, 1)
	captureErrors := make(chan error, 1)
	captureUnavailable := make(chan struct{}, 1)
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		lastTransientLog := time.Time{}
		for {
			if captureCtx.Err() != nil {
				return
			}
			frame, err := getFrame()
			if err != nil {
				if shouldRetry != nil && shouldRetry(err) {
					select {
					case captureUnavailable <- struct{}{}:
					default:
					}
					if time.Since(lastTransientLog) >= 5*time.Second {
						log.Printf("[gamecast/%s] frame temporarily unavailable; keeping send clock active: %v", mode, err)
						lastTransientLog = time.Now()
					}
					continue
				}
				select {
				case captureErrors <- err:
				default:
				}
				return
			}
			offerLatestGameCastVideoFrame(latestFrames, frame)
		}
	}()
	activeEncoder := encoders[0]
	for _, extra := range encoders[1:] {
		if extra.encoder != nil {
			_ = extra.encoder.Close()
		}
	}
	var pendingSwitch <-chan gameCastEncoderPreparation
	switchCtx, cancelSwitch := context.WithCancel(ctx)
	defer func() {
		cancelSwitch()
		cancelCapture()
		select {
		case <-captureDone:
		case <-time.After(500 * time.Millisecond):
			log.Printf("[gamecast/%s] capture worker did not stop within 500ms", mode)
		}
		if stopCapture != nil {
			_ = stopCapture()
		}
		if activeEncoder.encoder != nil {
			_ = activeEncoder.encoder.Close()
		}
		if pendingSwitch != nil {
			select {
			case prepared := <-pendingSwitch:
				if prepared.entry.encoder != nil {
					_ = prepared.entry.encoder.Close()
				}
			default:
			}
		}
	}()

	requestedProfile := adaptiveProfileFromLayer(activeEncoder.layer)
	controller := adaptive.NewController(requestedProfile, time.Now())
	localMetrics := adaptive.NewLocalMetrics(time.Now())
	qualityUpdates := make(chan gameCastQualityUpdate, 1)
	a.mu.Lock()
	participantID := a.gamecastParticipantID
	a.mu.Unlock()
	go a.runGameCastQualityPoller(ctx, session.roomID, participantID, qualityUpdates)
	var qualityUpdate gameCastQualityUpdate
	frameDuration := time.Second / time.Duration(maxInt(activeEncoder.layer.FPS, 1))
	nextFrameAt := time.Now()
	servedKeyFrameSeq := a.mediaKeyFrameSeq.Load()
	keyFrameGate := newGameCastKeyFrameGate(time.Now())
	txStats := newWebRTCVideoTxStats()
	var lastFrame *video.Frame
	budgetRetryAfter := time.Time{}

	for {
		frameDuration = time.Second / time.Duration(maxInt(activeEncoder.layer.FPS, 1))
		if !waitForWebRTCFrameSlot(ctx, &nextFrameAt, frameDuration) {
			return
		}
		select {
		case err := <-captureErrors:
			if err != nil && ctx.Err() == nil && onFatalCaptureError != nil {
				go onFatalCaptureError(err)
			}
			return
		default:
		}
		a.applyGameCastVideoKeyFrameRequest([]gameCastVideoEncoder{activeEncoder}, &servedKeyFrameSeq, keyFrameGate)
		for {
			select {
			case update := <-qualityUpdates:
				qualityUpdate = update
			default:
				goto qualityDrained
			}
		}
	qualityDrained:
		for {
			select {
			case <-captureUnavailable:
				localMetrics.RecordCaptureUnavailable()
			default:
				goto captureUnavailableDrained
			}
		}
	captureUnavailableDrained:

		var latestFrame *video.Frame
	drainLatest:
		for {
			select {
			case latestFrame = <-latestFrames:
			default:
				break drainLatest
			}
		}
		if latestFrame != nil {
			localMetrics.RecordCapture(latestFrame.Timestamp, time.Now())
		}
		lastFrame = chooseGameCastVideoFrame(lastFrame, latestFrame)
		if lastFrame == nil {
			continue
		}

		sentPrepared := false
		preparedBytes := 0
		if pendingSwitch != nil {
			select {
			case prepared := <-pendingSwitch:
				pendingSwitch = nil
				if prepared.err != nil {
					if prepared.decision != nil {
						controller.Reject(*prepared.decision, time.Now(), "prepare-failed")
					} else {
						budgetRetryAfter = time.Now().Add(30 * time.Second)
					}
					log.Printf("[gamecast/adaptive] prepare failed target=%s: %v", formatAdaptiveProfile(prepared.profile), prepared.err)
				} else {
					preparedDuration := time.Second / time.Duration(maxInt(prepared.profile.FPS, 1))
					writeErr := host.WriteH264LayerSample("f", prepared.encodedIDR, preparedDuration)
					if writeErr != nil {
						_ = prepared.entry.encoder.Close()
						if prepared.decision != nil {
							controller.Reject(*prepared.decision, time.Now(), "idr-write-failed")
						} else {
							budgetRetryAfter = time.Now().Add(30 * time.Second)
						}
						log.Printf("[gamecast/adaptive] prepared IDR write failed target=%s: %v", formatAdaptiveProfile(prepared.profile), writeErr)
					} else {
						old := activeEncoder
						activeEncoder = prepared.entry
						if old.encoder != nil {
							_ = old.encoder.Close()
						}
						if prepared.decision != nil {
							controller.Commit(*prepared.decision)
						}
						nextFrameAt = time.Now().Add(preparedDuration)
						localMetrics.RecordEncode(prepared.encodeTime, len(prepared.encodedIDR), latestFrame == nil, true)
						preparedBytes = len(prepared.encodedIDR)
						log.Printf("[gamecast/adaptive] switched target=%s", formatAdaptiveProfile(prepared.profile))
						sentPrepared = true
					}
				}
			default:
			}
		}

		encodedBytes := 0
		if !sentPrepared {
			encodeStarted := time.Now()
			encoded, encodeErr := activeEncoder.encoder.Encode(prepareWebRTCFrameForEncode(lastFrame, activeEncoder.layer.Width, activeEncoder.layer.Height))
			encodeDuration := time.Since(encodeStarted)
			if encodeErr != nil {
				localMetrics.RecordEncode(encodeDuration, 0, latestFrame == nil, false)
				log.Printf("[gamecast/%s] encode layer=%s failed: %v", mode, activeEncoder.layer.RID, encodeErr)
			} else if writeErr := host.WriteH264LayerSample(activeEncoder.layer.RID, encoded, frameDuration); writeErr != nil {
				localMetrics.RecordEncode(encodeDuration, len(encoded), latestFrame == nil, false)
				log.Printf("[gamecast/%s] write layer=%s failed: %v", mode, activeEncoder.layer.RID, writeErr)
			} else {
				encodedBytes = len(encoded)
				localMetrics.RecordEncode(encodeDuration, encodedBytes, latestFrame == nil, true)
			}
		} else {
			encodedBytes = preparedBytes
		}
		if encodedBytes > 0 {
			a.emitLocalShareStats(txStats.record(encodedBytes, host.SessionCount(), "single-track-"+mode, activeEncoder.layer.Width, activeEncoder.layer.Height), "gamecast-host")
		}

		now := time.Now()
		activeProfile := adaptiveProfileFromLayer(activeEncoder.layer)
		localSample, sampleReady := localMetrics.Snapshot(now, activeProfile)
		if !sampleReady {
			continue
		}
		viewers := qualityViewerSamples(qualityUpdate)
		decision := controller.Observe(now, &localSample, viewers)
		status := controller.Status(now)
		log.Printf(
			"[gamecast/adaptive] state=%s current=%s fresh_fps=%.1f encoded_fps=%.1f repeated=%.2f encode_budget=%.2f viewers=%d weak=%d reason=%s",
			status.State,
			formatAdaptiveProfile(activeProfile),
			localSample.FreshFPS,
			localSample.EncodedFPS,
			localSample.RepeatedRatio,
			localSample.EncodeBudgetRatio,
			status.ViewerCount,
			status.WeakViewers,
			status.Reason,
		)
		a.emitGameCastAdaptiveStats(localSample, status, activeProfile, host.SessionCount(), "single-track-"+mode)

		if pendingSwitch == nil && decision != nil {
			target := effectiveAdaptiveProfile(decision.To, qualityUpdate.snapshot.SafeVideoEgressKbps, maxInt(status.ViewerCount, host.SessionCount()))
			copyDecision := *decision
			log.Printf("[gamecast/adaptive] decision=%s reason=%s from=%s to=%s", decision.Type, decision.Reason, formatAdaptiveProfile(decision.From), formatAdaptiveProfile(target))
			pendingSwitch = prepareGameCastEncoderAsync(switchCtx, target, &copyDecision, lastFrame)
			continue
		}
		if pendingSwitch == nil && !now.Before(budgetRetryAfter) {
			budgetTarget := effectiveAdaptiveProfile(controller.Current(), qualityUpdate.snapshot.SafeVideoEgressKbps, maxInt(status.ViewerCount, host.SessionCount()))
			if !profilesEqual(budgetTarget, activeProfile) && budgetTarget.Width == activeProfile.Width && budgetTarget.Height == activeProfile.Height && budgetTarget.FPS == activeProfile.FPS {
				log.Printf("[gamecast/adaptive] budget reconfigure from=%s to=%s", formatAdaptiveProfile(activeProfile), formatAdaptiveProfile(budgetTarget))
				pendingSwitch = prepareGameCastEncoderAsync(switchCtx, budgetTarget, nil, lastFrame)
			}
		}
	}
}

func (a *App) runGameCastNativeWindowVideoLoop(
	ctx context.Context,
	host gameCastNativePublisher,
	capturer *hostwindowcapture.Capturer,
	encoders []gameCastVideoEncoder,
	session *shareSession,
	done chan<- struct{},
) {
	a.runGameCastVideoLoop(
		ctx,
		host,
		capturer.GetFrame,
		capturer.Stop,
		encoders,
		session,
		"window-capture",
		shouldRetryGameCastWindowCapture,
		func(err error) {
			log.Printf("[gamecast/window-capture] capture stopped: %v", err)
			a.handleGameCastNativeVideoFailure(session, err)
		},
		done,
	)
}

func (a *App) applyGameCastVideoKeyFrameRequest(encoders []gameCastVideoEncoder, servedSeq *int64, gate *gameCastKeyFrameGate) {
	if servedSeq == nil {
		return
	}
	seq := a.mediaKeyFrameSeq.Load()
	if seq == *servedSeq {
		return
	}
	*servedSeq = seq
	if gate != nil && !gate.Allow(time.Now()) {
		log.Printf("[gamecast/native-host] coalesced H264 keyframe request seq=%d", seq)
		return
	}
	for _, entry := range encoders {
		if entry.encoder == nil {
			continue
		}
		if err := entry.encoder.ForceKeyFrame(); err != nil {
			log.Printf("[gamecast/native-host] force H264 keyframe layer=%s seq=%d failed: %v", entry.layer.RID, seq, err)
		}
	}
	log.Printf("[gamecast/native-host] force H264 keyframe seq=%d", seq)
}

func (a *App) startWebRTCAudioLoop(manager *media.Manager, target audioCaptureTarget) error {
	if manager == nil {
		return fmt.Errorf("WebRTC audio loop requires an active media manager")
	}
	if target.Mode != windowsaudio.CaptureSystemMix && target.PID == 0 {
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
		Mode:            target.Mode,
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

	log.Printf("[share/audio] started %s target=%s:%d codec=opus", audioCaptureModeLabel(target.Mode), target.Name, target.PID)
	go a.runWebRTCAudioLoop(loopCtx, manager, capturer, opusEncoder, done)
	return nil
}

func (a *App) startGameCastAudioLoop(host gameCastNativePublisher, session *shareSession, target audioCaptureTarget) error {
	if host == nil {
		return fmt.Errorf("GameCast audio loop requires an active host")
	}
	if target.Mode != windowsaudio.CaptureSystemMix && target.PID == 0 {
		return fmt.Errorf("GameCast audio loop requires a process target")
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
		Mode:            target.Mode,
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

	log.Printf("[gamecast/audio] started %s target=%s:%d codec=opus", audioCaptureModeLabel(target.Mode), target.Name, target.PID)
	go a.runOpusAudioLoop(loopCtx, host, capturer, opusEncoder, done, func(err error) {
		go a.handleGameCastNativeAudioFailure(session, err)
	})
	return nil
}

func (a *App) runWebRTCAudioLoop(
	ctx context.Context,
	manager *media.Manager,
	capturer *windowsaudio.ProcessCapturer,
	encoder *audio.OpusEncoder,
	done chan<- struct{},
) {
	a.runOpusAudioLoop(ctx, manager, capturer, encoder, done, nil)
}

type opusSampleSink interface {
	WriteOpusSample(data []byte, duration time.Duration) error
}

type gameCastPCMSource interface {
	Frames() <-chan windowsaudio.PCMFrame
	Errors() <-chan error
	Stop()
}

type gameCastOpusEncoder interface {
	EncodePCM16(samples []int16) ([][]byte, error)
	FrameDuration() time.Duration
	Close() error
}

func (a *App) runOpusAudioLoop(
	ctx context.Context,
	sink opusSampleSink,
	capturer gameCastPCMSource,
	encoder gameCastOpusEncoder,
	done chan<- struct{},
	onFailure func(error),
) {
	defer close(done)
	defer capturer.Stop()
	defer encoder.Close()

	frames := capturer.Frames()
	errors := capturer.Errors()
	ticker := time.NewTicker(audio.DefaultOpusFrameMillis * time.Millisecond)
	defer ticker.Stop()
	frameValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
	pendingSamples := make([]int16, 0, frameValues*2)
	reportFailure := func(err error) {
		if err != nil && ctx.Err() == nil && onFailure != nil {
			onFailure(err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errors:
			if ok && err != nil {
				log.Printf("[share/audio] capture failed: %v", err)
				reportFailure(err)
			} else if !ok && ctx.Err() == nil {
				reportFailure(fmt.Errorf("shared audio capture ended unexpectedly"))
			}
			return
		case frame, ok := <-frames:
			if !ok {
				reportFailure(fmt.Errorf("shared audio source stopped producing frames"))
				return
			}
			samples, err := gameCastAudioSamplesFromFrame(&frame)
			if err != nil {
				log.Printf("[share/audio] dropping invalid PCM frame: %v", err)
				continue
			}
			pendingSamples = append(pendingSamples, samples...)
		case <-ticker.C:
			samples, nextPending, framesOpen, err := audioSamplesForOpusTick(frames, pendingSamples, frameValues)
			pendingSamples = nextPending
			if err != nil {
				log.Printf("[share/audio] dropping invalid PCM frame: %v", err)
				continue
			}
			if !framesOpen {
				reportFailure(fmt.Errorf("shared audio source stopped producing frames"))
				return
			}
			packets, err := encoder.EncodePCM16(samples)
			if err != nil {
				log.Printf("[share/audio] opus encode failed: %v", err)
				reportFailure(fmt.Errorf("encode shared audio: %w", err))
				return
			}
			for _, packet := range packets {
				if err := sink.WriteOpusSample(packet, encoder.FrameDuration()); err != nil {
					log.Printf("[share/audio] write opus sample failed: %v", err)
					reportFailure(fmt.Errorf("send shared audio: %w", err))
					return
				}
			}
		}
	}
}

func audioSamplesForOpusTick(
	frames <-chan windowsaudio.PCMFrame,
	pendingSamples []int16,
	frameValues int,
) ([]int16, []int16, bool, error) {
	if frameValues <= 0 {
		return nil, pendingSamples, true, fmt.Errorf("Opus frame size must be positive")
	}
	for len(pendingSamples) < frameValues {
		select {
		case frame, ok := <-frames:
			if !ok {
				return nil, pendingSamples, false, nil
			}
			samples, err := gameCastAudioSamplesFromFrame(&frame)
			if err != nil {
				return nil, pendingSamples, true, err
			}
			pendingSamples = append(pendingSamples, samples...)
		default:
			samples := make([]int16, frameValues)
			return samples, pendingSamples, true, nil
		}
	}

	samples := make([]int16, frameValues)
	copy(samples, pendingSamples[:frameValues])
	return samples, pendingSamples[frameValues:], true, nil
}

func gameCastAudioSamplesForTick(frame *windowsaudio.PCMFrame) ([]int16, error) {
	if frame == nil {
		frameValues := audio.DefaultOpusSampleRate * audio.DefaultOpusFrameMillis / 1000 * audio.DefaultOpusChannels
		return make([]int16, frameValues), nil
	}
	return gameCastAudioSamplesFromFrame(frame)
}

func gameCastAudioSamplesFromFrame(frame *windowsaudio.PCMFrame) ([]int16, error) {
	if frame == nil {
		return nil, nil
	}
	if frame.SampleRate != audio.DefaultOpusSampleRate || frame.Channels != audio.DefaultOpusChannels {
		return nil, fmt.Errorf(
			"unexpected PCM format sample_rate=%d channels=%d",
			frame.SampleRate,
			frame.Channels,
		)
	}
	return audio.PCM16BytesToSamples(frame.Data)
}

func audioCaptureModeLabel(mode windowsaudio.CaptureMode) string {
	switch mode {
	case windowsaudio.CaptureSystemMix:
		return "system-loopback"
	case windowsaudio.CaptureExcludeProcessTree:
		return "process-exclude-tree"
	default:
		return "process-loopback"
	}
}

type h264SampleSink interface {
	WriteH264Sample(data []byte, duration time.Duration) error
	SessionCount() int
}

func (a *App) runWebRTCVideoLoop(
	ctx context.Context,
	sink h264SampleSink,
	statsPeerID string,
	capturer *video.NativeCapturer,
	encoder *video.NativeEncoder,
	targetWidth int,
	targetHeight int,
	fps int,
	allowDirectNative bool,
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
	directNativePath := allowDirectNative
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
				if err := sink.WriteH264Sample(encoded, frameDuration); err != nil {
					log.Printf("[media/webrtc] write H264 sample failed: %v", err)
				}
				a.emitLocalShareStats(txStats.record(len(encoded), sink.SessionCount(), "direct", targetWidth, targetHeight), statsPeerID)
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
		if err := sink.WriteH264Sample(encoded, frameDuration); err != nil {
			log.Printf("[media/webrtc] write H264 sample failed: %v", err)
		}
		a.emitLocalShareStats(txStats.record(len(encoded), sink.SessionCount(), "cpu-resize", targetWidth, targetHeight), statsPeerID)
	}
}

func (a *App) runGameCastWindowVideoLoop(
	ctx context.Context,
	sink h264SampleSink,
	capturer *hostwindowcapture.Capturer,
	encoder *video.NativeEncoder,
	session *shareSession,
	done chan<- struct{},
) {
	defer close(done)
	defer func() {
		_ = encoder.Close()
		_ = capturer.Stop()
	}()

	frameDuration := time.Second / time.Duration(maxInt(session.fps, 1))
	nextFrameAt := time.Now()
	servedKeyFrameSeq := a.mediaKeyFrameSeq.Load()
	txStats := newWebRTCVideoTxStats()
	lastTransientLog := time.Time{}

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

		frame, err := capturer.GetFrame()
		if err != nil {
			if shouldRetryGameCastWindowCapture(err) {
				if time.Since(lastTransientLog) >= 5*time.Second {
					log.Printf("[gamecast/window-capture] frame temporarily unavailable; retrying: %v", err)
					lastTransientLog = time.Now()
				}
				continue
			}
			if ctx.Err() == nil {
				log.Printf("[gamecast/window-capture] capture stopped: %v", err)
				go a.handleGameCastNativeVideoFailure(session, err)
			}
			return
		}
		frame = prepareWebRTCFrameForEncode(frame, session.width, session.height)

		encoded, err := encoder.Encode(frame)
		if err != nil {
			log.Printf("[gamecast/window-capture] encode failed: %v", err)
			continue
		}
		if err := sink.WriteH264Sample(encoded, frameDuration); err != nil {
			log.Printf("[gamecast/window-capture] write sample failed: %v", err)
		}
		a.emitLocalShareStats(txStats.record(len(encoded), sink.SessionCount(), "window-capture", session.width, session.height), "gamecast-host")
	}
}

func shouldRetryGameCastWindowCapture(err error) bool {
	return errors.Is(err, hostwindowcapture.ErrFrameUnavailable)
}

func shouldRetryGameCastDisplayCapture(err error) bool {
	return errors.Is(err, video.ErrCaptureFrameUnavailable)
}

func (a *App) currentGameCastNativeShare(session *shareSession) bool {
	if session == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gamecastNativeShare == session
}

func (a *App) tryRebindGameCastWindowAudio(host gameCastNativePublisher, session *shareSession) bool {
	if host == nil || session == nil {
		return false
	}
	a.mu.Lock()
	current := a.gamecastNativeShare == session && a.gamecastNativeHost == host
	eligible := current &&
		session.sourceKind == string(sharecatalog.SourceKindWindow) &&
		!session.audioRebindAttempted &&
		stableGameCastAudioIdentity(session.audioTarget)
	original := session.audioTarget
	if eligible {
		session.audioRebindAttempted = true
	}
	a.mu.Unlock()
	if !eligible {
		return false
	}

	refreshed, enabled, mode := a.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{}, session)
	if !enabled || mode != "process" || !sameStableGameCastAudioApplication(original, refreshed) {
		log.Printf("[gamecast/audio] runtime rebind target rejected room=%s original_pid=%d", session.roomID, original.PID)
		return false
	}

	a.mu.Lock()
	current = a.gamecastNativeShare == session && a.gamecastNativeHost == host
	a.mu.Unlock()
	if !current {
		return false
	}
	start := a.gamecastAudioLoopStarter
	if start == nil {
		start = a.startGameCastAudioLoop
	}
	if err := start(host, session, refreshed); err != nil {
		log.Printf("[gamecast/audio] runtime rebind failed room=%s target=%s:%d reason=%s: %v", session.roomID, refreshed.Name, refreshed.PID, refreshed.Reason, err)
		return false
	}

	a.mu.Lock()
	current = a.gamecastNativeShare == session && a.gamecastNativeHost == host
	if current {
		session.audioTarget = refreshed
		session.audioMode = mode
	}
	a.mu.Unlock()
	if !current {
		a.stopAudioMediaLoop()
		return false
	}
	log.Printf("[gamecast/audio] runtime rebind succeeded room=%s target=%s:%d reason=%s", session.roomID, refreshed.Name, refreshed.PID, refreshed.Reason)
	return true
}

func waitForGameCastShareReady(session *shareSession) {
	if session != nil && session.ready != nil {
		<-session.ready
	}
}

func (a *App) handleGameCastNativeVideoFailure(session *shareSession, cause error) {
	waitForGameCastShareReady(session)
	a.gamecastRuntimeMu.Lock()
	if !a.currentGameCastNativeShare(session) {
		a.gamecastRuntimeMu.Unlock()
		return
	}

	sourceName := "窗口"
	if session.sourceKind == string(sharecatalog.SourceKindDisplay) {
		sourceName = "屏幕"
	}
	message := fmt.Sprintf("原生 GameCast %s捕获意外停止，共享已自动结束。", sourceName)
	if errors.Is(cause, hostwindowcapture.ErrWindowClosed) {
		message = "共享窗口已关闭，原生 GameCast 共享已自动停止。"
	}
	log.Printf("[gamecast/native-host] stopping failed %s share room=%s: %v", sourceName, session.roomID, cause)
	a.stopGameCastNativeHostLocked()
	a.gamecastRuntimeMu.Unlock()
	if _, err := a.SetGameCastHostSharing(GameCastHostShareRequest{
		RoomID:    session.roomID,
		Sharing:   false,
		PeerID:    session.peerID,
		Width:     session.width,
		Height:    session.height,
		FPS:       session.fps,
		AudioMode: "off",
	}); err != nil {
		log.Printf("[gamecast/native-host] publish stopped room state failed: %v", err)
	}
	a.emitGameCastShareRuntime(app.GameCastShareRuntimeEvent{
		Kind:      "video-stopped",
		RoomID:    session.roomID,
		PeerID:    session.peerID,
		AudioMode: "off",
		Message:   message,
	})
}

func (a *App) handleGameCastNativeAudioFailure(session *shareSession, cause error) {
	waitForGameCastShareReady(session)
	a.gamecastRuntimeMu.Lock()
	defer a.gamecastRuntimeMu.Unlock()
	if !a.currentGameCastNativeShare(session) {
		return
	}

	log.Printf("[gamecast/audio] disabling failed audio source room=%s mode=%s: %v", session.roomID, session.audioMode, cause)
	a.stopAudioMediaLoop()
	if !a.currentGameCastNativeShare(session) {
		return
	}
	a.mu.Lock()
	host := a.gamecastNativeHost
	a.mu.Unlock()
	if a.tryRebindGameCastWindowAudio(host, session) {
		return
	}
	if !a.currentGameCastNativeShare(session) {
		return
	}
	a.mu.Lock()
	session.audioMode = "off"
	a.mu.Unlock()
	if _, err := a.SetGameCastHostSharing(GameCastHostShareRequest{
		RoomID:    session.roomID,
		Sharing:   true,
		PeerID:    session.peerID,
		Width:     session.width,
		Height:    session.height,
		FPS:       session.fps,
		AudioMode: "off",
	}); err != nil {
		log.Printf("[gamecast/audio] publish audio-disabled room state failed: %v", err)
	}
	a.emitGameCastShareRuntime(app.GameCastShareRuntimeEvent{
		Kind:      "audio-disabled",
		RoomID:    session.roomID,
		PeerID:    session.peerID,
		AudioMode: "off",
		Message:   "共享音频源已失效，音频已自动关闭，视频共享仍在继续。",
	})
}

func (a *App) emitGameCastShareRuntime(event app.GameCastShareRuntimeEvent) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventGameCastShareRuntime), event)
}

func (a *App) emitLocalShareStats(snapshot *webRTCVideoTxSnapshot, peerID string) {
	if a.ctx == nil || snapshot == nil || peerID == "" {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventStreamStats), app.StreamStatsEvent{
		PeerID:             peerID,
		Width:              snapshot.Width,
		Height:             snapshot.Height,
		Mode:               snapshot.Mode,
		EncodedFPS:         snapshot.EncodedFPS,
		EstimatedSourceFPS: snapshot.EstimatedSourceFPS,
		BitrateKbps:        snapshot.BitrateKbps,
		Sessions:           snapshot.Sessions,
	})
}

func shouldUseDirectNativeEncode(sourceWidth, sourceHeight, targetWidth, targetHeight int) bool {
	return sourceWidth > 0 &&
		sourceHeight > 0 &&
		targetWidth > 0 &&
		targetHeight > 0 &&
		sourceWidth == targetWidth &&
		sourceHeight == targetHeight
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

type webRTCVideoTxSnapshot struct {
	Mode               string
	Width              int
	Height             int
	EncodedFPS         float64
	EstimatedSourceFPS float64
	BitrateKbps        float64
	Sessions           int
}

func (s *webRTCVideoTxStats) record(encodedBytes int, sessions int, mode string, width int, height int) *webRTCVideoTxSnapshot {
	s.encodedFrames++
	s.encodedBytes += encodedBytes
	s.lastSessions = sessions
	if sessions > 0 {
		s.sampleWrites += sessions
	} else {
		s.noSessionSamples++
	}

	if time.Since(s.lastLog) < 2*time.Second {
		return nil
	}
	snapshot := s.snapshot(mode, width, height)
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
	return snapshot
}

func (s *webRTCVideoTxStats) snapshot(mode string, width int, height int) *webRTCVideoTxSnapshot {
	elapsed := time.Since(s.lastLog).Seconds()
	if elapsed <= 0 || s.encodedFrames == 0 {
		return nil
	}
	encodedFPS := float64(s.encodedFrames) / elapsed
	return &webRTCVideoTxSnapshot{
		Mode:               mode,
		Width:              width,
		Height:             height,
		EncodedFPS:         encodedFPS,
		EstimatedSourceFPS: encodedFPS,
		BitrateKbps:        float64(s.encodedBytes*8) / elapsed / 1000.0,
		Sessions:           s.lastSessions,
	}
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
	if !waitForMediaLoopDone(done, gameCastMediaStopTimeout) {
		log.Printf("[share/audio] stop timed out after %s; continuing shutdown", gameCastMediaStopTimeout)
	}
}

func (a *App) stopGameCastMediaLoop() {
	a.mu.Lock()
	cancel := a.gamecastMediaCancel
	done := a.gamecastMediaDone
	a.gamecastMediaCancel = nil
	a.gamecastMediaDone = nil
	a.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if !waitForMediaLoopDone(done, gameCastMediaStopTimeout) {
		log.Printf("[gamecast/native-host] video loop stop timed out after %s; continuing shutdown", gameCastMediaStopTimeout)
	}
}

func waitForMediaLoopDone(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	if timeout <= 0 {
		<-done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (a *App) stopGameCastNativeHost() {
	a.gamecastRuntimeMu.Lock()
	defer a.gamecastRuntimeMu.Unlock()
	a.stopGameCastNativeHostLocked()
}

func (a *App) stopGameCastNativeHostLocked() {
	a.stopGameCastMediaLoop()
	a.stopAudioMediaLoop()

	a.mu.Lock()
	host := a.gamecastNativeHost
	a.gamecastNativeHost = nil
	a.gamecastNativeShare = nil
	a.gamecastP2PPublisher = nil
	a.mu.Unlock()

	if host != nil {
		if err := host.Close(); err != nil {
			log.Printf("[gamecast/native-host] close failed: %v", err)
		}
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

func maxInt(a, b int) int {
	if a > b {
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

func (a *App) GetShareSources() []sharecatalog.Source {
	return sharecatalog.ListSources()
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
			Mode: windowsaudio.CaptureIncludeProcessTree,
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
			Mode: windowsaudio.CaptureIncludeProcessTree,
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
		return fmt.Errorf("请先离开房间，再重新应用认证密钥。")
	}

	if err := a.restartNetwork(authKey); err != nil {
		return fmt.Errorf("restart network: %w", err)
	}
	return nil
}

func (a *App) GetGameCastProfile() GameCastProfile {
	a.mu.Lock()
	defer a.mu.Unlock()
	return gameCastProfileForUI(a.cfg)
}

func (a *App) SaveGameCastProfile(profile GameCastProfile) (GameCastProfile, error) {
	normalized, err := gamecast.NormalizeBaseURL(profile.BaseURL)
	if err != nil {
		return GameCastProfile{}, err
	}

	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	oldBaseURL, _ := gamecast.NormalizeBaseURL(a.cfg.GameCast.BaseURL)
	a.mu.Unlock()
	if oldBaseURL != normalized {
		if err := a.stopGameCastScreenShare(); err != nil {
			log.Printf("[gamecast/profile] stop share before server change failed: %v", err)
		}
		a.closeGameCastWS()
		a.closeGameCastP2PRoom()
		a.stopGameCastViewerIntent(true)
		a.stopGameCastPresence(true)
		a.stopGameCastRoomHeartbeat(true)
	}

	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.GameCast.BaseURL = normalized
	a.cfg.GameCast.DisplayName = normalizeDisplayName(profile.DisplayName)
	if strings.TrimSpace(profile.TrustedCertificateSHA256) != "" {
		a.cfg.GameCast.TrustedCertificateSHA256 = gamecast.NormalizeFingerprint(profile.TrustedCertificateSHA256)
	} else if oldBaseURL != normalized {
		a.cfg.GameCast.TrustedCertificateSHA256 = ""
	}
	cfg := a.cfg
	next := gameCastProfileForUI(cfg)
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return GameCastProfile{}, fmt.Errorf("save gamecast profile: %w", err)
	}
	if oldBaseURL != normalized {
		if err := a.ensureGameCastJar(gameCastProfileFromConfig(cfg)); err != nil {
			log.Printf("reload gamecast session jar failed: %v", err)
		}
	}
	return next, nil
}

func (a *App) ProbeGameCastServer(baseURL string) GameCastProbeResult {
	profile := a.gameCastProfileForRequest(baseURL)
	client, err := a.gameCastClient(profile)
	if err != nil {
		return GameCastProbeResult{
			OK:      false,
			Status:  "error",
			BaseURL: profile.BaseURL,
			Message: err.Error(),
		}
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return client.Probe(probeCtx)
}

func (a *App) TrustGameCastServerCertificate(baseURL, fingerprint string) (GameCastProfile, error) {
	normalized, err := gamecast.NormalizeBaseURL(baseURL)
	if err != nil {
		return GameCastProfile{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	actual, err := gamecast.FetchCertificateFingerprint(probeCtx, normalized)
	if err != nil {
		return GameCastProfile{}, err
	}
	if actual == "" {
		return a.SaveGameCastProfile(GameCastProfile{BaseURL: normalized})
	}
	if fingerprint != "" && !gamecast.EqualFingerprint(actual, fingerprint) {
		return GameCastProfile{}, fmt.Errorf("certificate fingerprint changed while trusting server")
	}

	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.GameCast.BaseURL = normalized
	a.cfg.GameCast.TrustedCertificateSHA256 = actual
	cfg := a.cfg
	next := gameCastProfileForUI(cfg)
	a.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return GameCastProfile{}, fmt.Errorf("save gamecast trust: %w", err)
	}
	if err := a.ensureGameCastJar(gameCastProfileFromConfig(cfg)); err != nil {
		log.Printf("reload gamecast session jar failed: %v", err)
	}
	return next, nil
}

func (a *App) GetGameCastSession() (GameCastSessionState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return GameCastSessionState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cfg, err := client.GetServerConfig(requestCtx)
	if err != nil {
		return GameCastSessionState{}, err
	}
	return gameCastSessionStateFromConfig(cfg), nil
}

func (a *App) LoginGameCast(credentials GameCastCredentials) (GameCastSessionState, error) {
	if strings.TrimSpace(credentials.User) == "" || credentials.Pass == "" {
		return GameCastSessionState{}, fmt.Errorf("username and password are required")
	}
	profile := a.gameCastProfileForRequest("")
	client, err := a.gameCastClient(profile)
	if err != nil {
		return GameCastSessionState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cfg, err := client.Login(requestCtx, credentials.User, credentials.Pass)
	if err != nil {
		return GameCastSessionState{}, err
	}
	if err := a.saveGameCastJar(profile); err != nil {
		log.Printf("save gamecast session jar failed: %v", err)
	}
	return gameCastSessionStateFromConfig(cfg), nil
}

func (a *App) RegisterGameCast(credentials GameCastCredentials) (GameCastSessionState, error) {
	if strings.TrimSpace(credentials.User) == "" || credentials.Pass == "" {
		return GameCastSessionState{}, fmt.Errorf("username and password are required")
	}
	profile := a.gameCastProfileForRequest("")
	client, err := a.gameCastClient(profile)
	if err != nil {
		return GameCastSessionState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Register(requestCtx, credentials.User, credentials.Pass); err != nil {
		return GameCastSessionState{}, err
	}
	cfg, err := client.Login(requestCtx, credentials.User, credentials.Pass)
	if err != nil {
		return GameCastSessionState{}, err
	}
	if err := a.saveGameCastJar(profile); err != nil {
		log.Printf("save gamecast session jar failed: %v", err)
	}
	return gameCastSessionStateFromConfig(cfg), nil
}

func (a *App) LogoutGameCast() error {
	profile := a.gameCastProfileForRequest("")
	if err := a.stopGameCastScreenShare(); err != nil {
		log.Printf("[gamecast/logout] stop share failed: %v", err)
	}
	a.closeGameCastWS()
	a.closeGameCastP2PRoom()
	a.stopGameCastViewerIntent(true)
	a.stopGameCastPresence(true)
	a.stopGameCastRoomHeartbeat(true)
	client, err := a.gameCastClient(profile)
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := client.Logout(requestCtx); err != nil {
		return err
	}
	a.mu.Lock()
	jar := a.gamecastJar
	a.mu.Unlock()
	if jar != nil {
		if err := jar.Clear(profile.BaseURL); err != nil {
			log.Printf("clear gamecast session jar failed: %v", err)
		}
	}
	return nil
}

func (a *App) GetGameCastRoomState(roomID string) (GameCastRoomState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return GameCastRoomState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return client.GetRoomState(requestCtx, roomID)
}

func (a *App) GetGameCastRooms() ([]GameCastRoomState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return nil, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return client.ListRooms(requestCtx)
}

func (a *App) GetGameCastParticipantID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gamecastParticipantID
}

func (a *App) GetGameCastPresence(roomID string) ([]GameCastPresenceParticipant, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return nil, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	participants, _, err := client.ListPresence(requestCtx, roomID)
	return participants, err
}

func (a *App) GetGameCastHistory() ([]gamecast.HistoryEntry, error) {
	profile := a.gameCastProfileForRequest("")
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	a.mu.Lock()
	local := localGameCastHistoryLocked(a.cfg)
	a.mu.Unlock()

	client, err := a.gameCastClient(profile)
	if err != nil {
		return local, nil
	}
	remote, err := client.GetHistory(requestCtx)
	if err != nil {
		return local, nil
	}
	merged := rooms.MergeHistory(local, remote)
	a.persistGameCastHistory(merged, true)
	return merged, nil
}

func (a *App) AddGameCastHistory(roomID string) ([]gamecast.HistoryEntry, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return a.GetGameCastHistory()
	}
	a.mu.Lock()
	current := localGameCastHistoryLocked(a.cfg)
	next := rooms.AddHistory(current, roomID, time.Now())
	a.mu.Unlock()
	a.persistGameCastHistory(next, true)
	return next, nil
}

func (a *App) RemoveGameCastHistory(roomID string) ([]gamecast.HistoryEntry, error) {
	roomID = strings.TrimSpace(roomID)
	a.mu.Lock()
	current := localGameCastHistoryLocked(a.cfg)
	next := make([]gamecast.HistoryEntry, 0, len(current))
	for _, entry := range current {
		if !strings.EqualFold(entry.ID, roomID) {
			next = append(next, entry)
		}
	}
	a.mu.Unlock()
	a.persistGameCastHistory(next, true)
	return next, nil
}

func (a *App) CreateGameCastRoom() (GameCastRoomSession, error) {
	sessionState, err := a.GetGameCastSession()
	if err != nil {
		return GameCastRoomSession{}, err
	}
	if !sessionState.LoggedIn {
		return GameCastRoomSession{}, fmt.Errorf("please log in before creating a GameCast room")
	}
	roomID := rooms.PersonalRoomID(sessionState.User)
	if roomID == "" {
		return GameCastRoomSession{}, fmt.Errorf("current GameCast user cannot be used as a room id")
	}
	return a.enterGameCastRoom(roomID, "member", "client")
}

func (a *App) EnterGameCastHostRoom(roomID string) (GameCastRoomSession, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return GameCastRoomSession{}, fmt.Errorf("room id is required")
	}
	return a.enterGameCastHostRoom(roomID)
}

func (a *App) JoinGameCastRoom(roomID string) (GameCastRoomSession, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return GameCastRoomSession{}, fmt.Errorf("room id is required")
	}
	return a.enterGameCastRoom(roomID, "member", "")
}

func (a *App) LeaveGameCastRoom() error {
	shareErr := a.stopGameCastScreenShare()
	a.closeGameCastP2PRoom()
	a.stopGameCastPresence(true)
	a.stopGameCastRoomHeartbeat(true)
	a.stopGameCastViewerIntent(true)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, string(app.EventRoomLeft), nil)
	}
	return shareErr
}

func (a *App) ConnectGameCastHost(roomID string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	a.closeGameCastWS()
	generation := a.nextGameCastWSGeneration()
	a.stopGameCastViewerIntent(true)
	a.stopGameCastNativeHost()
	client := a.newGameCastWSClient(roomID, "host", "", generation)
	if !a.setGameCastWSIfCurrent(generation, client) {
		return fmt.Errorf("gamecast websocket connection was superseded")
	}
	if err := client.Connect(ctx); err != nil {
		a.clearGameCastWSIfCurrent(generation, client)
		return err
	}
	if !a.gameCastWSIsCurrent(generation, client) {
		_ = client.Close()
		return fmt.Errorf("gamecast websocket connection was superseded")
	}
	if _, err := a.AddGameCastHistory(roomID); err != nil {
		log.Printf("add gamecast history failed: %v", err)
	}
	return nil
}

func (a *App) SetGameCastHostSharing(request GameCastHostShareRequest) (GameCastRoomState, error) {
	roomID := strings.TrimSpace(request.RoomID)
	if roomID == "" {
		a.mu.Lock()
		if a.cfg != nil {
			roomID = a.cfg.LastRoomID
		}
		a.mu.Unlock()
	}
	if roomID == "" {
		return GameCastRoomState{}, fmt.Errorf("room id is required")
	}
	if a.gameCastPresenceManagesRoom(roomID) {
		posted, err := a.setPresenceManagedGameCastSharing(roomID, request.Sharing, request.AudioMode)
		if err != nil {
			return GameCastRoomState{}, err
		}
		a.emitGameCastStreamState(request)
		return posted, nil
	}
	if !request.Sharing {
		if err := a.releaseGameCastShareLease(); err != nil && !isGameCastLeaseGone(err) {
			log.Printf("[gamecast/share-lease] release failed room=%s: %v", roomID, err)
		}
	}
	audioMode := strings.TrimSpace(request.AudioMode)
	if audioMode == "" {
		audioMode = "off"
	}
	state := GameCastRoomState{
		Room:      roomID,
		Active:    true,
		Origin:    "client",
		Sharing:   request.Sharing,
		SFU:       request.Sharing,
		AudioMode: audioMode,
		User:      a.gameCastUserLabel(),
		TTL:       15,
	}
	posted, err := a.postGameCastRoomState(state)
	if err != nil {
		return GameCastRoomState{}, err
	}
	a.stopGameCastRoomHeartbeat(false)
	a.startGameCastRoomHeartbeat(posted)

	a.emitGameCastStreamState(request)
	return posted, nil
}

func (a *App) gameCastPresenceManagesRoom(roomID string) bool {
	a.mu.Lock()
	active := a.gamecastPresenceCancel != nil
	presenceRoom := a.gamecastPresenceRoom
	a.mu.Unlock()
	return presenceManagesGameCastRoom(active, presenceRoom, roomID)
}

func presenceManagesGameCastRoom(active bool, presenceRoom, roomID string) bool {
	return active && strings.EqualFold(strings.TrimSpace(presenceRoom), strings.TrimSpace(roomID))
}

func (a *App) setPresenceManagedGameCastSharing(roomID string, sharing bool, audioMode string) (GameCastRoomState, error) {
	audioMode = strings.TrimSpace(audioMode)
	if audioMode == "" {
		audioMode = "off"
	}
	if !sharing {
		stopErr := a.stopGameCastScreenShare()
		state, stateErr := a.GetGameCastRoomState(roomID)
		return state, errors.Join(stopErr, stateErr)
	}

	a.mu.Lock()
	hasLease := a.gamecastShareLease != nil && strings.EqualFold(a.gamecastShareLease.Room, roomID)
	a.mu.Unlock()
	var lease gamecast.ShareLease
	var state GameCastRoomState
	var err error
	if hasLease {
		lease, state, err = a.heartbeatGameCastShare(roomID, gamecast.SharePhaseLive, audioMode)
	} else {
		if _, _, err = a.claimGameCastShare(roomID, audioMode); err == nil {
			lease, state, err = a.heartbeatGameCastShare(roomID, gamecast.SharePhaseLive, audioMode)
		}
	}
	if err != nil {
		return GameCastRoomState{}, err
	}
	a.startGameCastShareHeartbeat(lease, gamecast.SharePhaseLive, audioMode)
	return state, nil
}

func (a *App) emitGameCastStreamState(request GameCastHostShareRequest) {
	if a.ctx == nil {
		return
	}
	event := app.StreamEvent{
		PeerID: gameCastSharePeerID(request),
		Width:  request.Width,
		Height: request.Height,
		FPS:    request.FPS,
	}
	if request.Sharing {
		runtime.EventsEmit(a.ctx, string(app.EventStreamStarted), event)
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventStreamStopped), event)
}

func gameCastSharePeerID(request GameCastHostShareRequest) string {
	if peerID := strings.TrimSpace(request.PeerID); peerID != "" {
		return peerID
	}
	return "gamecast-host"
}

func (a *App) StartNativeGameCastSharing(request GameCastHostShareRequest) (GameCastRoomState, error) {
	roomID := strings.TrimSpace(request.RoomID)
	if roomID == "" {
		a.mu.Lock()
		if a.cfg != nil {
			roomID = a.cfg.LastRoomID
		}
		a.mu.Unlock()
	}
	if roomID == "" {
		return GameCastRoomState{}, fmt.Errorf("room id is required")
	}
	if !a.hostVideoCapability.SupportsHost || a.hostVideoCapability.Backend != video.BackendNative {
		logVideoCapability("[video/capability] gamecast-native-share-rejected", a.hostVideoCapability)
		return GameCastRoomState{}, fmt.Errorf("native GameCast sharing requires DXGI/NVENC support: %s", a.hostVideoCapability.Reason)
	}

	sourceKind := strings.TrimSpace(request.SourceKind)
	if sourceKind == "" {
		sourceKind = string(sharecatalog.SourceKindDisplay)
	}
	displays := video.ListDisplays()
	displayIndex := 0
	windowHandle := uintptr(0)
	windowProcessID := uint32(0)
	windowProcessName := ""
	sourceWidth := 0
	sourceHeight := 0

	switch sourceKind {
	case string(sharecatalog.SourceKindDisplay):
		var err error
		displayIndex, err = resolveDisplayShareSelection(request.SourceID, request.SourceKind, len(displays))
		if err != nil {
			return GameCastRoomState{}, err
		}
		if displayIndex < 0 || displayIndex >= len(displays) {
			return GameCastRoomState{}, fmt.Errorf("selected display is unavailable")
		}
		bounds := displays[displayIndex]
		sourceWidth = bounds.Dx()
		sourceHeight = bounds.Dy()
	case string(sharecatalog.SourceKindWindow):
		var err error
		windowHandle, sourceWidth, sourceHeight, windowProcessID, windowProcessName, err = resolveWindowShareSelection(request.SourceID)
		if err != nil {
			return GameCastRoomState{}, err
		}
	default:
		return GameCastRoomState{}, fmt.Errorf("unsupported share source type")
	}

	width, height := resolveShareResolution(sourceWidth, sourceHeight, request.Width, request.Height)
	fps := normalizeShareFPS(request.FPS)
	ready := make(chan struct{})
	session := &shareSession{
		roomID:           roomID,
		peerID:           gameCastSharePeerID(request),
		displayIndex:     displayIndex,
		windowHandle:     windowHandle,
		sourceKind:       sourceKind,
		sourceLabel:      strings.TrimSpace(request.SourceLabel),
		audioProcessID:   windowProcessID,
		audioProcessName: windowProcessName,
		width:            width,
		height:           height,
		fps:              fps,
		audioMode:        "off",
		ready:            ready,
	}
	readyClosed := false
	defer func() {
		if !readyClosed {
			close(ready)
		}
	}()

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeGameCastWS()
	a.stopGameCastViewerIntent(true)
	a.stopGameCastNativeHost()
	lease, _, err := a.claimGameCastShare(roomID, "off")
	if err != nil {
		return GameCastRoomState{}, fmt.Errorf("claim gamecast share: %w", err)
	}
	leaseCommitted := false
	defer func() {
		if !leaseCommitted {
			_ = a.releaseGameCastShareLease()
		}
	}()
	mediaMode := strings.TrimSpace(lease.MediaMode)
	if mediaMode != gamecast.MediaModeLegacyP2P && mediaMode != gamecast.MediaModeSFU {
		return GameCastRoomState{}, fmt.Errorf("unsupported GameCast media mode %q", mediaMode)
	}
	heartbeatPhase := gamecast.SharePhaseStarting
	if mediaMode == gamecast.MediaModeLegacyP2P {
		heartbeatPhase = gamecast.SharePhaseLive
	}
	a.startGameCastShareHeartbeat(lease, heartbeatPhase, "off")

	audioTarget, audioEnabled, audioMode := a.resolveGameCastAudioCaptureTarget(request, session)

	hostReady := make(chan error, 1)
	var hostReadyOnce sync.Once
	reportHostReady := func(err error) {
		hostReadyOnce.Do(func() {
			hostReady <- err
		})
	}
	onStatus := func(status gamecastws.Status) {
		a.emitGameCastStatus(status)
		if resolved, readyErr := gameCastNativeHostReadyResult(status); resolved {
			reportHostReady(readyErr)
		}
	}
	onKeyFrameRequest := func(reason string) {
		seq := a.mediaKeyFrameSeq.Add(1)
		log.Printf("[gamecast/native-host] keyframe requested reason=%s seq=%d", reason, seq)
	}
	var host gameCastNativePublisher
	if mediaMode == gamecast.MediaModeLegacyP2P {
		a.mu.Lock()
		signaler := a.gamecastP2PRoom
		a.mu.Unlock()
		if signaler == nil {
			return GameCastRoomState{}, fmt.Errorf("native Screego room is not connected")
		}
		publisher := screegop2p.NewPublisher(screegop2p.PublisherConfig{
			Signaler:          signaler,
			Width:             width,
			Height:            height,
			FPS:               fps,
			AudioEnabled:      audioEnabled,
			OnStatus:          onStatus,
			OnKeyFrameRequest: onKeyFrameRequest,
		})
		host = publisher
		a.mu.Lock()
		a.gamecastNativeHost = publisher
		a.gamecastNativeShare = session
		a.gamecastP2PPublisher = publisher
		a.mu.Unlock()
		if err := publisher.Start(ctx); err != nil {
			a.stopGameCastNativeHost()
			return GameCastRoomState{}, err
		}
	} else {
		sfuHost := nativehost.New(nativehost.Config{
			Profile:      a.gameCastProfileForRequest(""),
			RoomID:       roomID,
			Name:         a.gameCastUserLabel(),
			Width:        width,
			Height:       height,
			FPS:          fps,
			AudioEnabled: audioEnabled,
			OnAuxMessage: func(message json.RawMessage) {
				a.emitGameCastSignal(message)
			},
			OnStatus:          onStatus,
			OnKeyFrameRequest: onKeyFrameRequest,
		})
		if err := sfuHost.Start(ctx); err != nil {
			return GameCastRoomState{}, err
		}
		host = sfuHost
		a.mu.Lock()
		a.gamecastNativeHost = sfuHost
		a.gamecastNativeShare = session
		a.mu.Unlock()
	}
	if err := a.startGameCastVideoLoop(host, session); err != nil {
		a.stopGameCastNativeHost()
		return GameCastRoomState{}, err
	}
	var audioStartErr error
	if audioEnabled {
		audioMode, audioEnabled, audioStartErr = a.startGameCastAudioLoopOrDisable(host, session, audioTarget, audioMode)
	}
	session.audioMode = audioMode
	readyCtx, cancelReady := context.WithTimeout(ctx, gameCastHostReadyTimeout)
	defer cancelReady()
	select {
	case <-readyCtx.Done():
		a.stopGameCastNativeHost()
		return GameCastRoomState{}, fmt.Errorf("wait for native GameCast media readiness: %w", readyCtx.Err())
	case readyErr := <-hostReady:
		if readyErr != nil {
			a.stopGameCastNativeHost()
			return GameCastRoomState{}, readyErr
		}
	}

	posted, err := a.SetGameCastHostSharing(GameCastHostShareRequest{
		RoomID:    roomID,
		Sharing:   true,
		PeerID:    session.peerID,
		Width:     width,
		Height:    height,
		FPS:       fps,
		AudioMode: audioMode,
	})
	if err != nil {
		a.stopGameCastNativeHost()
		return GameCastRoomState{}, err
	}
	leaseCommitted = true
	close(ready)
	readyClosed = true
	if audioStartErr != nil {
		a.emitGameCastShareRuntime(app.GameCastShareRuntimeEvent{
			Kind:      "audio-disabled",
			RoomID:    session.roomID,
			PeerID:    session.peerID,
			AudioMode: "off",
			Message:   "无法启动选定的共享音频源，已继续使用无音频的视频共享。",
		})
	}
	return posted, nil
}

func gameCastNativeHostReadyResult(status gamecastws.Status) (bool, error) {
	switch {
	case status.State == "connected" &&
		(status.Detail == "native host sending media" || status.Detail == "native P2P host sharing"):
		return true, nil
	case status.State == "error":
		return true, fmt.Errorf("native GameCast host failed before media was ready: %s", status.Detail)
	case status.State == "closed":
		return true, fmt.Errorf("native GameCast host closed before media was ready")
	default:
		return false, nil
	}
}

func (a *App) startGameCastAudioLoopOrDisable(
	host gameCastNativePublisher,
	session *shareSession,
	primary audioCaptureTarget,
	audioMode string,
) (string, bool, error) {
	start := func(target audioCaptureTarget) error {
		if a.gamecastAudioLoopStarter != nil {
			return a.gamecastAudioLoopStarter(host, session, target)
		}
		return a.startGameCastAudioLoop(host, session, target)
	}
	mode, enabled, firstErr := startGameCastAudioTarget(start, primary, audioMode)
	if firstErr == nil {
		a.storeGameCastAudioTarget(session, primary, mode, false)
		return mode, enabled, nil
	}
	log.Printf(
		"[gamecast/audio] selected audio start failed mode=%s target=%s:%d reason=%s: %v",
		audioCaptureModeLabel(primary.Mode),
		primary.Name,
		primary.PID,
		primary.Reason,
		firstErr,
	)

	if session == nil || session.sourceKind != string(sharecatalog.SourceKindWindow) || !stableGameCastAudioIdentity(primary) {
		return "off", false, firstErr
	}
	refreshed, retryEnabled, retryMode := a.resolveGameCastAudioCaptureTarget(GameCastHostShareRequest{}, session)
	if !retryEnabled || retryMode != "process" || !sameStableGameCastAudioApplication(primary, refreshed) {
		log.Printf("[gamecast/audio] refreshed target rejected after initial failure target=%s:%d", primary.Name, primary.PID)
		return "off", false, firstErr
	}

	mode, enabled, retryErr := startGameCastAudioTarget(start, refreshed, retryMode)
	if retryErr != nil {
		log.Printf(
			"[gamecast/audio] refreshed audio start failed; continuing video-only target=%s:%d reason=%s: %v",
			refreshed.Name,
			refreshed.PID,
			refreshed.Reason,
			retryErr,
		)
		return "off", false, errors.Join(firstErr, retryErr)
	}
	a.storeGameCastAudioTarget(session, refreshed, mode, false)
	log.Printf("[gamecast/audio] refreshed target started target=%s:%d reason=%s", refreshed.Name, refreshed.PID, refreshed.Reason)
	return mode, enabled, nil
}

func stableGameCastAudioIdentity(target audioCaptureTarget) bool {
	return target.Mode == windowsaudio.CaptureIncludeProcessTree &&
		target.PID != 0 &&
		target.IdentityStable &&
		strings.TrimSpace(target.ApplicationIdentity) != ""
}

func sameStableGameCastAudioApplication(left, right audioCaptureTarget) bool {
	return stableGameCastAudioIdentity(left) &&
		stableGameCastAudioIdentity(right) &&
		strings.EqualFold(strings.TrimSpace(left.ApplicationIdentity), strings.TrimSpace(right.ApplicationIdentity))
}

func (a *App) storeGameCastAudioTarget(session *shareSession, target audioCaptureTarget, mode string, rebindAttempted bool) {
	if session == nil {
		return
	}
	a.mu.Lock()
	session.audioTarget = target
	session.audioMode = mode
	session.audioRebindAttempted = rebindAttempted
	a.mu.Unlock()
}

func startGameCastAudioTarget(
	start func(audioCaptureTarget) error,
	target audioCaptureTarget,
	audioMode string,
) (string, bool, error) {
	if err := start(target); err != nil {
		return "off", false, err
	}
	return audioMode, true, nil
}

func resolveDisplayShareSelection(sourceID, sourceKind string, displayCount int) (int, error) {
	normalizedKind := strings.TrimSpace(sourceKind)
	if normalizedKind == "" || normalizedKind == string(sharecatalog.SourceKindDisplay) {
		if strings.TrimSpace(sourceID) == "" {
			return 0, nil
		}
		var displayIndex int
		if _, err := fmt.Sscanf(strings.TrimSpace(sourceID), "display:%d", &displayIndex); err != nil {
			return 0, fmt.Errorf("invalid display source selection")
		}
		if displayIndex < 0 || displayIndex >= displayCount {
			return 0, fmt.Errorf("selected display is unavailable")
		}
		return displayIndex, nil
	}
	if normalizedKind == string(sharecatalog.SourceKindWindow) {
		return 0, fmt.Errorf("窗口共享界面已就绪，但原生窗口捕获仍在接入中；请先选择整个屏幕共享。")
	}
	return 0, fmt.Errorf("unsupported share source type")
}

func resolveWindowShareSelection(sourceID string) (uintptr, int, int, uint32, string, error) {
	if strings.TrimSpace(sourceID) == "" {
		return 0, 0, 0, 0, "", fmt.Errorf("window share source is required")
	}
	var handle uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(sourceID), "window:%d", &handle); err != nil || handle == 0 {
		return 0, 0, 0, 0, "", fmt.Errorf("invalid window source selection")
	}
	capturer := hostwindowcapture.New(uintptr(handle), defaultShareFPS)
	bounds, err := capturer.PrepareForShare()
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	process, err := sharecatalog.ResolveWindowProcess(uintptr(handle))
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	return uintptr(handle), bounds.Dx(), bounds.Dy(), process.ProcessID, process.ProcessName, nil
}

func (a *App) resolveGameCastAudioCaptureTarget(_ GameCastHostShareRequest, session *shareSession) (audioCaptureTarget, bool, string) {
	if session == nil {
		return audioCaptureTarget{}, false, "off"
	}
	if !a.audioCapability.SupportsProcessCapture {
		log.Printf("[gamecast/audio] process loopback unavailable: %s", a.audioCapability.Reason)
		return audioCaptureTarget{}, false, "off"
	}
	opusCapability := audio.QueryOpusCapability()
	if !opusCapability.Available {
		log.Printf("[gamecast/audio] opus unavailable: %s", opusCapability.Reason)
		return audioCaptureTarget{}, false, "off"
	}

	switch session.sourceKind {
	case string(sharecatalog.SourceKindDisplay):
		return audioCaptureTarget{
			PID:  uint32(os.Getpid()),
			Name: "PartyLAN process tree",
			Mode: windowsaudio.CaptureExcludeProcessTree,
		}, true, "exclude-process"
	case string(sharecatalog.SourceKindWindow):
		resolveWindowProcess := a.windowProcessResolver
		if resolveWindowProcess == nil {
			resolveWindowProcess = sharecatalog.ResolveWindowProcess
		}
		windowProcess, err := resolveWindowProcess(session.windowHandle)
		if err != nil || windowProcess.ProcessID == 0 {
			log.Printf("[gamecast/audio] selected window process unavailable hwnd=%d: %v", session.windowHandle, err)
			return audioCaptureTarget{}, false, "off"
		}
		resolver := a.gamecastShareAudioResolver
		if resolver == nil {
			log.Printf("[gamecast/audio] safe window audio resolver unavailable hwnd=%d pid=%d", session.windowHandle, windowProcess.ProcessID)
			return audioCaptureTarget{}, false, "off"
		}
		resolved, err := resolver.ResolveWindow(shareaudio.WindowTarget{
			WindowPID:   windowProcess.ProcessID,
			PartyLANPID: uint32(os.Getpid()),
		})
		if err != nil {
			log.Printf("[gamecast/audio] safe window audio target rejected hwnd=%d pid=%d: %v", session.windowHandle, windowProcess.ProcessID, err)
			return audioCaptureTarget{}, false, "off"
		}
		identity := strings.TrimSpace(resolved.Identity)
		if resolved.ProcessID == 0 || resolved.Mode != windowsaudio.CaptureIncludeProcessTree || !resolved.IdentityStable || identity == "" {
			log.Printf(
				"[gamecast/audio] invalid window audio target disabled hwnd=%d window_pid=%d target_pid=%d mode=%s stable_identity=%t reason=%s",
				session.windowHandle,
				windowProcess.ProcessID,
				resolved.ProcessID,
				audioCaptureModeLabel(resolved.Mode),
				resolved.IdentityStable,
				resolved.Reason,
			)
			return audioCaptureTarget{}, false, "off"
		}
		processName := strings.TrimSpace(windowProcess.ProcessName)
		if processName == "" {
			processName = fmt.Sprintf("PID %d", resolved.ProcessID)
		}
		log.Printf(
			"[gamecast/audio] resolved window audio hwnd=%d window_pid=%d target_pid=%d reason=%s",
			session.windowHandle,
			windowProcess.ProcessID,
			resolved.ProcessID,
			resolved.Reason,
		)
		return audioCaptureTarget{
			PID:                 resolved.ProcessID,
			Name:                processName,
			Mode:                windowsaudio.CaptureIncludeProcessTree,
			ApplicationIdentity: identity,
			IdentityStable:      true,
			Reason:              resolved.Reason,
		}, true, "process"
	}
	return audioCaptureTarget{}, false, "off"
}

func processTreeMembership(processID, rootProcessID uint32, lookup func(int) (ps.Process, error)) (bool, bool) {
	if processID == 0 || rootProcessID == 0 || lookup == nil {
		return false, false
	}
	visited := make(map[uint32]struct{}, 8)
	current := processID
	for depth := 0; depth < 64 && current != 0; depth++ {
		if current == rootProcessID {
			return true, true
		}
		if _, exists := visited[current]; exists {
			return false, true
		}
		visited[current] = struct{}{}
		process, err := lookup(int(current))
		if err != nil || process == nil {
			return false, false
		}
		if process.PPid() <= 0 {
			return false, true
		}
		current = uint32(process.PPid())
	}
	return false, current == 0
}

func processBelongsToTree(processID, rootProcessID uint32, lookup func(int) (ps.Process, error)) bool {
	belongs, _ := processTreeMembership(processID, rootProcessID, lookup)
	return belongs
}

func selectGameCastProcessAudioTarget(
	processID uint32,
	processName string,
	resolveByName func(string) (audioCaptureTarget, bool),
) (audioCaptureTarget, bool) {
	processName = strings.TrimSpace(processName)
	if processID != 0 {
		if processName == "" {
			processName = fmt.Sprintf("PID %d", processID)
		}
		return audioCaptureTarget{
			PID:  processID,
			Name: processName,
			Mode: windowsaudio.CaptureIncludeProcessTree,
		}, true
	}
	if resolveByName == nil {
		return audioCaptureTarget{}, false
	}
	return resolveByName(processName)
}

func (a *App) resolveAudioCaptureTargetByName(name string) (audioCaptureTarget, bool) {
	canonical := canonicalAudioProcessName(name)
	if canonical == "" {
		return audioCaptureTarget{}, false
	}
	sources, err := windowsaudio.ListActiveAudioSources()
	if err == nil {
		targets := selectedAudioCaptureTargetsFromAudioSources(sources, []string{canonical})
		if len(targets) > 0 {
			return targets[0], true
		}
	}
	processes, err := ps.Processes()
	if err == nil {
		targets := selectedAudioCaptureTargetsFromProcesses(processes, []string{canonical})
		if len(targets) > 0 {
			return targets[0], true
		}
	}
	return audioCaptureTarget{}, false
}

func (a *App) ConnectGameCastViewer(roomID, layer string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	layer = normalizeGameCastLayer(layer)
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	presenceRoom := a.gamecastPresenceRoom
	presenceActive := a.gamecastPresenceCancel != nil
	participantID := a.gamecastParticipantID
	a.mu.Unlock()
	var roomState GameCastRoomState
	if !presenceActive || !strings.EqualFold(presenceRoom, roomID) {
		_, joinedState, err := a.startGameCastPresence(roomID, "member", "")
		if err != nil {
			return fmt.Errorf("join gamecast presence: %w", err)
		}
		roomState = joinedState
	} else {
		client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
		if err != nil {
			return err
		}
		requestCtx, cancelRequest := context.WithTimeout(ctx, 5*time.Second)
		roomState, err = client.GetRoomState(requestCtx, roomID)
		cancelRequest()
		if err != nil {
			return err
		}
	}

	if !gameCastRoomLiveForViewer(roomState, participantID) {
		a.stopGameCastViewerIntent(true)
		a.closeGameCastWS()
		return nil
	}
	if roomState.MediaMode == gamecast.MediaModeLegacyP2P {
		a.closeGameCastWS()
		a.stopGameCastViewerIntent(true)
		if _, err := a.AddGameCastHistory(roomID); err != nil {
			log.Printf("add gamecast history failed: %v", err)
		}
		return nil
	}
	a.closeGameCastWS()
	generation := a.nextGameCastWSGeneration()
	a.stopGameCastViewerIntent(true)
	client := a.newGameCastWSClient(roomID, "viewer", layer, generation)
	if !a.setGameCastWSIfCurrent(generation, client) {
		return fmt.Errorf("gamecast websocket connection was superseded")
	}
	if err := client.Connect(ctx); err != nil {
		a.clearGameCastWSIfCurrent(generation, client)
		return err
	}
	if !a.gameCastWSIsCurrent(generation, client) {
		_ = client.Close()
		return fmt.Errorf("gamecast websocket connection was superseded")
	}
	if _, err := a.AddGameCastHistory(roomID); err != nil {
		log.Printf("add gamecast history failed: %v", err)
	}
	return nil
}

func gameCastRoomLiveForViewer(state GameCastRoomState, participantID string) bool {
	if !state.Active || !state.Sharing || state.SharePhase != gamecast.SharePhaseLive ||
		(strings.TrimSpace(state.ShareOwnerParticipantID) != "" && state.ShareOwnerParticipantID == participantID) {
		return false
	}
	switch state.MediaMode {
	case gamecast.MediaModeLegacyP2P:
		return !state.SFU
	case gamecast.MediaModeSFU:
		return state.MediaProtocolVersion == gamecast.MediaProtocolVersion && state.SFU
	default:
		return false
	}
}

func (a *App) PostGameCastPlaybackStats(payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	qualityReport, hasQualityReport := gameCastQualityReportFromPayload(payload)
	payload["client"] = "partylan"
	payload["user"] = a.gameCastUserLabel()
	payload["reportedAt"] = time.Now().UTC().Format(time.RFC3339Nano)

	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if hasQualityReport {
		if err := client.ReportQuality(requestCtx, qualityReport); err != nil {
			a.mu.Lock()
			shouldLog := time.Since(a.gamecastQualityFailureLog) >= 10*time.Second
			if shouldLog {
				a.gamecastQualityFailureLog = time.Now()
			}
			a.mu.Unlock()
			if shouldLog {
				log.Printf("[gamecast/quality] report failed room=%s participant=%s: %v", qualityReport.Room, qualityReport.ParticipantID, err)
			}
		}
	}
	return client.PostStats(requestCtx, payload)
}

func gameCastQualityReportFromPayload(payload map[string]interface{}) (gamecast.QualityReport, bool) {
	if payload == nil || payload["valid"] != true || strings.TrimSpace(fmt.Sprint(payload["role"])) != "viewer" {
		return gamecast.QualityReport{}, false
	}
	text := func(key string) string {
		value, ok := payload[key].(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(value)
	}
	number := func(key string) (float64, bool) {
		value, ok := payload[key]
		if !ok {
			return 0, false
		}
		switch typed := value.(type) {
		case float64:
			return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
		case float32:
			converted := float64(typed)
			return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		default:
			return 0, false
		}
	}
	room := text("room")
	participantID := text("participantId")
	generation := text("generation")
	pairID := text("pairId")
	received, receivedOK := number("packetsReceivedDelta")
	lost, lostOK := number("packetsLostDelta")
	dropped, droppedOK := number("framesDroppedDelta")
	decodedFPS, decodedOK := number("decodedFps")
	bitrate, bitrateOK := number("incomingBitrateKbps")
	jitter, jitterOK := number("jitterMs")
	if room == "" || participantID == "" || generation == "" || pairID == "" ||
		!receivedOK || !lostOK || !droppedOK || !decodedOK || !bitrateOK || !jitterOK ||
		received < 0 || lost < 0 || dropped < 0 || decodedFPS < 0 || bitrate < 0 || jitter < 0 {
		return gamecast.QualityReport{}, false
	}
	return gamecast.QualityReport{
		Room:                 room,
		ParticipantID:        participantID,
		Generation:           generation,
		PairID:               pairID,
		PacketsReceivedDelta: int64(received),
		PacketsLostDelta:     int64(lost),
		FramesDroppedDelta:   int64(dropped),
		DecodedFPS:           decodedFPS,
		IncomingBitrateKbps:  bitrate,
		JitterMS:             jitter,
	}, true
}

func (a *App) newGameCastWSClient(roomID, role, layer string, generation uint64) *gamecastws.Client {
	var client *gamecastws.Client
	client = gamecastws.NewClient(gamecastws.Options{
		Profile: a.gameCastProfileForRequest(""),
		RoomID:  roomID,
		Role:    role,
		Layer:   layer,
		Name:    a.gameCastUserLabel(),
		OnMessage: func(message json.RawMessage) {
			a.withCurrentGameCastWSClient(generation, client, func() {
				a.emitGameCastSignal(message)
			})
		},
		OnStatus: func(status gamecastws.Status) {
			a.withCurrentGameCastWSClient(generation, client, func() {
				a.emitGameCastStatus(status)
			})
		},
	})
	return client
}

func (a *App) nextGameCastWSGeneration() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gamecastWSGeneration++
	return a.gamecastWSGeneration
}

func (a *App) isCurrentGameCastWSGeneration(generation uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gamecastWSGeneration == generation
}

func (a *App) withCurrentGameCastWSGeneration(generation uint64, callback func()) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gamecastWSGeneration != generation {
		return false
	}
	callback()
	return true
}

func (a *App) withCurrentGameCastWSClient(
	generation uint64,
	client *gamecastws.Client,
	callback func(),
) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gamecastWSGeneration != generation || a.gamecastWS != client {
		return false
	}
	callback()
	return true
}

func (a *App) setGameCastWSIfCurrent(generation uint64, client *gamecastws.Client) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gamecastWSGeneration != generation || a.gamecastWS != nil {
		return false
	}
	a.gamecastWS = client
	return true
}

func (a *App) clearGameCastWSIfCurrent(generation uint64, client *gamecastws.Client) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gamecastWSGeneration != generation || a.gamecastWS != client {
		return false
	}
	a.gamecastWS = nil
	return true
}

func (a *App) gameCastWSIsCurrent(generation uint64, client *gamecastws.Client) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gamecastWSGeneration == generation && a.gamecastWS == client
}

func (a *App) DisconnectGameCast() error {
	a.stopGameCastNativeHost()
	a.closeGameCastWS()
	a.closeGameCastP2PRoom()
	a.stopGameCastViewerIntent(true)
	return nil
}

func (a *App) connectGameCastP2PRoom(roomID string, owner bool) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	a.closeGameCastP2PRoom()
	profile := a.gameCastProfileForRequest("")
	if err := a.ensureGameCastJar(profile); err != nil {
		return err
	}
	a.mu.Lock()
	jar := a.gamecastJar
	a.mu.Unlock()
	var client *screegop2p.Client
	client = screegop2p.NewClient(screegop2p.Options{
		Profile: profile,
		Jar:     jar,
		RoomID:  roomID,
		Name:    a.gameCastUserLabel(),
		Owner:   owner,
		OnMessage: func(message screegop2p.IncomingMessage) {
			a.handleGameCastP2PMessage(client, message)
		},
		OnStatus: func(status gamecastws.Status) {
			a.handleGameCastP2PStatus(client, status)
		},
	})
	viewer := screegop2p.NewViewer(screegop2p.ViewerConfig{
		Signaler: client,
		OnSignal: a.emitGameCastSignal,
		OnStatus: a.emitGameCastStatus,
	})
	a.mu.Lock()
	if a.gamecastP2PRoom != nil || a.gamecastP2PViewer != nil {
		a.mu.Unlock()
		return fmt.Errorf("gamecast P2P room connection was superseded")
	}
	a.gamecastP2PRoom = client
	a.gamecastP2PViewer = viewer
	a.mu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := client.Connect(ctx); err != nil {
		a.mu.Lock()
		if a.gamecastP2PRoom == client {
			a.gamecastP2PRoom = nil
			a.gamecastP2PViewer = nil
		}
		a.mu.Unlock()
		viewer.Close()
		_ = client.Close()
		return err
	}
	return nil
}

func (a *App) closeGameCastP2PRoom() {
	a.mu.Lock()
	client := a.gamecastP2PRoom
	viewer := a.gamecastP2PViewer
	a.gamecastP2PRoom = nil
	a.gamecastP2PViewer = nil
	a.mu.Unlock()
	if viewer != nil {
		viewer.Close()
	}
	if client != nil {
		if err := client.Close(); err != nil {
			log.Printf("[gamecast/screego] close room failed: %v", err)
		}
	}
}

func (a *App) handleGameCastP2PMessage(source *screegop2p.Client, message screegop2p.IncomingMessage) {
	a.mu.Lock()
	if source == nil || a.gamecastP2PRoom != source {
		a.mu.Unlock()
		return
	}
	viewer := a.gamecastP2PViewer
	publisher := a.gamecastP2PPublisher
	a.mu.Unlock()
	if publisher != nil {
		if err := publisher.HandleMessage(message); err != nil {
			a.emitGameCastStatus(gamecastws.Status{State: "error", Detail: screegop2p.ErrorProtocol})
		}
	}
	if viewer != nil {
		if err := viewer.HandleMessage(message); err == nil {
			return
		}
		a.emitGameCastStatus(gamecastws.Status{State: "error", Detail: screegop2p.ErrorProtocol})
	}
}

func (a *App) handleGameCastP2PStatus(source *screegop2p.Client, status gamecastws.Status) {
	a.mu.Lock()
	current := source != nil && a.gamecastP2PRoom == source
	publisher := a.gamecastP2PPublisher
	a.mu.Unlock()
	if !current {
		return
	}
	a.emitGameCastStatus(status)
	if status.State == "connected" && publisher != nil {
		if err := publisher.Resume(); err != nil {
			a.emitGameCastStatus(gamecastws.Status{State: "error", Detail: screegop2p.ErrorUnavailable})
		}
	}
}

func (a *App) SendGameCastSignal(signal map[string]interface{}) error {
	payload, err := json.Marshal(signal)
	if err != nil {
		return fmt.Errorf("marshal gamecast signal: %w", err)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	a.mu.Lock()
	client := a.gamecastWS
	viewer := a.gamecastP2PViewer
	a.mu.Unlock()
	if sessionID, _ := signal["sessionId"].(string); strings.TrimSpace(sessionID) != "" {
		if viewer == nil {
			return fmt.Errorf("gamecast native P2P viewer is not connected")
		}
		return viewer.SendFrontendSignal(sendCtx, payload)
	}
	if client == nil {
		return fmt.Errorf("gamecast sfu is not connected")
	}
	return client.Send(sendCtx, payload)
}

func (a *App) closeGameCastWS() {
	a.mu.Lock()
	a.gamecastWSGeneration++
	client := a.gamecastWS
	a.gamecastWS = nil
	a.mu.Unlock()
	if client != nil {
		if err := client.Close(); err != nil {
			log.Printf("[gamecast/ws] close failed: %v", err)
		}
	}
}

func (a *App) enterGameCastHostRoom(roomID string) (GameCastRoomSession, error) {
	return a.enterGameCastRoom(roomID, "owner", "client")
}

func (a *App) enterGameCastRoom(roomID, requestedRole, origin string) (GameCastRoomSession, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return GameCastRoomSession{}, fmt.Errorf("room id is required")
	}
	a.closeGameCastP2PRoom()
	a.stopGameCastRoomHeartbeat(false)
	participant, presenceState, err := a.startGameCastPresence(roomID, requestedRole, origin)
	if err != nil {
		return GameCastRoomSession{}, fmt.Errorf("join gamecast presence: %w", err)
	}
	if presenceState.MediaMode == gamecast.MediaModeLegacyP2P {
		if err := a.connectGameCastP2PRoom(roomID, participant.Role == "owner"); err != nil {
			a.stopGameCastPresence(true)
			return GameCastRoomSession{}, fmt.Errorf("join native Screego room: %w", err)
		}
	}

	posted := presenceState
	posted.Room = roomID
	posted.Active = true
	if strings.TrimSpace(posted.Origin) == "" && strings.TrimSpace(origin) != "" {
		posted.Origin = strings.TrimSpace(origin)
	}
	if strings.TrimSpace(posted.User) == "" {
		posted.User = a.gameCastUserLabel()
	}
	if posted.TTL <= 0 {
		posted.TTL = 15
	}
	role := "viewer"
	selfPeerID := "gamecast-viewer"
	if participant.Role == "owner" {
		role = "host"
		selfPeerID = "gamecast-host"
	}
	a.rememberGameCastRoom(roomID)
	if _, err := a.AddGameCastHistory(roomID); err != nil {
		log.Printf("add gamecast history failed: %v", err)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, string(app.EventRoomJoined), map[string]interface{}{
			"roomId":     roomID,
			"selfPeerId": selfPeerID,
		})
	}
	return GameCastRoomSession{
		RoomID: roomID,
		Role:   role,
		State:  posted,
	}, nil
}

func (a *App) startGameCastRoomHeartbeat(state GameCastRoomState) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.gamecastRoomCancel = cancel
	a.mu.Unlock()

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if _, err := a.postGameCastRoomState(state); err != nil {
					log.Printf("[gamecast/room] heartbeat failed room=%s: %v", state.Room, err)
				}
			}
		}
	}()
}

func (a *App) stopGameCastRoomHeartbeat(markInactive bool) {
	a.mu.Lock()
	cancel := a.gamecastRoomCancel
	a.gamecastRoomCancel = nil
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if !markInactive {
		return
	}
	roomID := ""
	a.mu.Lock()
	if a.cfg != nil {
		roomID = a.cfg.LastRoomID
	}
	a.mu.Unlock()
	if strings.TrimSpace(roomID) == "" {
		return
	}
	_, err := a.postGameCastRoomState(GameCastRoomState{
		Room:    roomID,
		Active:  false,
		Sharing: false,
		SFU:     false,
		User:    a.gameCastUserLabel(),
		TTL:     15,
	})
	if err != nil {
		log.Printf("[gamecast/room] inactive post failed room=%s: %v", roomID, err)
	}
}

func (a *App) startGameCastViewerIntent(roomID string) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return
	}
	a.stopGameCastViewerIntent(true)

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.gamecastViewerIntentCancel = cancel
	a.gamecastViewerIntentRoom = roomID
	a.mu.Unlock()

	if err := a.postGameCastViewerIntent(roomID, true); err != nil {
		log.Printf("[gamecast/viewer-intent] initial post failed room=%s: %v", roomID, err)
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := a.postGameCastViewerIntent(roomID, true); err != nil {
					log.Printf("[gamecast/viewer-intent] heartbeat failed room=%s: %v", roomID, err)
				}
			}
		}
	}()
}

func (a *App) stopGameCastViewerIntent(markInactive bool) {
	a.mu.Lock()
	cancel := a.gamecastViewerIntentCancel
	roomID := a.gamecastViewerIntentRoom
	a.gamecastViewerIntentCancel = nil
	a.gamecastViewerIntentRoom = ""
	a.mu.Unlock()
	if cancel == nil && strings.TrimSpace(roomID) == "" {
		return
	}
	if cancel != nil {
		cancel()
	}
	if !markInactive || strings.TrimSpace(roomID) == "" {
		return
	}
	if err := a.postGameCastViewerIntent(roomID, false); err != nil {
		log.Printf("[gamecast/viewer-intent] inactive post failed room=%s: %v", roomID, err)
	}
}

func (a *App) postGameCastViewerIntent(roomID string, active bool) error {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return client.PostViewerIntent(requestCtx, gamecast.ViewerIntent{
		Room:     roomID,
		Active:   active,
		WantsSFU: active,
		Client:   "partylan",
		User:     a.gameCastUserLabel(),
		TTL:      15,
	})
}

func (a *App) gameCastPresenceRequest(roomID, role, origin string) gamecast.PresenceRequest {
	a.mu.Lock()
	participantID := a.gamecastParticipantID
	hostCapability := a.hostVideoCapability
	a.mu.Unlock()
	return gamecast.PresenceRequest{
		Room:                 strings.TrimSpace(roomID),
		ParticipantID:        participantID,
		ClientType:           "client",
		Origin:               strings.TrimSpace(origin),
		Role:                 strings.TrimSpace(role),
		Name:                 a.gameCastUserLabel(),
		MediaProtocolVersion: gamecast.MediaProtocolVersion,
		Capabilities: gamecast.MediaCapabilities{
			SFUViewer: true,
			SFUHost:   hostCapability.SupportsHost && hostCapability.Backend == video.BackendNative,
			H264:      true,
			Opus:      true,
		},
		TTL: 15,
	}
}

func (a *App) startGameCastPresence(roomID, role, origin string) (gamecast.PresenceParticipant, GameCastRoomState, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return gamecast.PresenceParticipant{}, GameCastRoomState{}, fmt.Errorf("room id is required")
	}
	a.stopGameCastPresence(true)
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return gamecast.PresenceParticipant{}, GameCastRoomState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request := a.gameCastPresenceRequest(roomID, role, origin)
	requestCtx, cancelRequest := context.WithTimeout(ctx, 5*time.Second)
	participant, roomState, err := client.JoinPresence(requestCtx, request)
	cancelRequest()
	if err != nil {
		return gamecast.PresenceParticipant{}, GameCastRoomState{}, err
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.gamecastPresenceCancel = cancel
	a.gamecastPresenceRoom = roomID
	a.gamecastPresenceRole = role
	a.gamecastPresenceOrigin = origin
	a.gamecastPresenceMediaMode = roomState.MediaMode
	a.mu.Unlock()
	a.startGameCastVoice(heartbeatCtx, client, request)
	go a.runGameCastPresenceHeartbeat(heartbeatCtx, request)
	return participant, roomState, nil
}

func (a *App) startGameCastVoice(ctx context.Context, client *gamecast.Client, request gamecast.PresenceRequest) {
	if client == nil || request.Room == "" || request.ParticipantID == "" || request.ClientType != "client" {
		return
	}
	var voiceSession roomVoiceSession
	config := gamecastvoice.Config{
		RoomID:            request.Room,
		ParticipantID:     request.ParticipantID,
		TokenIssuer:       client,
		ConnectionFactory: gamecastvoice.NewPionConnection,
		OnState: func(state gamecastvoice.State) {
			a.emitGameCastVoiceStateIfCurrent(voiceSession, state)
		},
	}
	a.mu.Lock()
	factory := a.gamecastVoiceFactory
	a.mu.Unlock()
	if factory == nil {
		factory = func(config gamecastvoice.Config) roomVoiceSession {
			return gamecastvoice.New(config)
		}
	}
	voiceSession = factory(config)
	if voiceSession == nil {
		a.emitGameCastVoiceState(gamecastvoice.State{
			Connection: gamecastvoice.ConnectionError,
			Microphone: gamecastvoice.MicrophoneUnavailable,
			ErrorCode:  "voice_session_unavailable",
		})
		return
	}

	a.stopGameCastVoice()
	a.mu.Lock()
	a.gamecastVoice = voiceSession
	a.mu.Unlock()
	if err := voiceSession.Start(ctx); err != nil {
		a.mu.Lock()
		if a.gamecastVoice == voiceSession {
			a.gamecastVoice = nil
		}
		a.mu.Unlock()
		voiceSession.Stop()
		a.emitGameCastVoiceState(gamecastvoice.State{
			Connection: gamecastvoice.ConnectionError,
			Microphone: gamecastvoice.MicrophoneUnavailable,
			ErrorCode:  "voice_session_start_failed",
		})
	}
}

func (a *App) stopGameCastVoice() {
	a.mu.Lock()
	voiceSession := a.gamecastVoice
	a.gamecastVoice = nil
	a.mu.Unlock()
	if voiceSession != nil {
		voiceSession.Stop()
	}
}

func (a *App) GetRoomVoiceState() gamecastvoice.State {
	a.mu.Lock()
	voiceSession := a.gamecastVoice
	a.mu.Unlock()
	if voiceSession == nil {
		return gamecastvoice.State{Connection: gamecastvoice.ConnectionIdle, Microphone: gamecastvoice.MicrophoneMuted}
	}
	return voiceSession.Snapshot()
}

func (a *App) SetRoomVoiceMicrophoneEnabled(enabled bool) gamecastvoice.State {
	a.mu.Lock()
	voiceSession := a.gamecastVoice
	a.mu.Unlock()
	if voiceSession == nil {
		return gamecastvoice.State{Connection: gamecastvoice.ConnectionIdle, Microphone: gamecastvoice.MicrophoneMuted}
	}
	return voiceSession.SetMicrophoneEnabled(enabled)
}

func (a *App) SetRoomVoicePlaybackMuted(muted bool) gamecastvoice.State {
	a.mu.Lock()
	voiceSession := a.gamecastVoice
	a.mu.Unlock()
	if voiceSession == nil {
		return gamecastvoice.State{Connection: gamecastvoice.ConnectionIdle, Microphone: gamecastvoice.MicrophoneMuted}
	}
	return voiceSession.SetPlaybackMuted(muted)
}

func (a *App) emitGameCastVoiceStateIfCurrent(voiceSession roomVoiceSession, state gamecastvoice.State) {
	a.mu.Lock()
	current := voiceSession != nil && a.gamecastVoice == voiceSession
	a.mu.Unlock()
	if current {
		a.emitGameCastVoiceState(state)
	}
}

func (a *App) emitGameCastVoiceState(state gamecastvoice.State) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, string(app.EventGameCastVoiceState), state)
	}
}

func (a *App) runGameCastPresenceHeartbeat(ctx context.Context, request gamecast.PresenceRequest) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	request.Name = ""
	request.Origin = ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
			if err != nil {
				log.Printf("[gamecast/presence] client unavailable room=%s: %v", request.Room, err)
				continue
			}
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err = client.HeartbeatPresence(requestCtx, request)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("[gamecast/presence] heartbeat failed room=%s: %v", request.Room, err)
			}
		}
	}
}

func (a *App) stopGameCastPresence(markInactive bool) {
	a.stopGameCastVoice()
	a.mu.Lock()
	cancel := a.gamecastPresenceCancel
	roomID := a.gamecastPresenceRoom
	role := a.gamecastPresenceRole
	a.gamecastPresenceCancel = nil
	a.gamecastPresenceRoom = ""
	a.gamecastPresenceRole = ""
	a.gamecastPresenceOrigin = ""
	a.gamecastPresenceMediaMode = ""
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !markInactive || strings.TrimSpace(roomID) == "" {
		return
	}
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		log.Printf("[gamecast/presence] leave client unavailable room=%s: %v", roomID, err)
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request := a.gameCastPresenceRequest(roomID, role, "")
	request.Name = ""
	requestCtx, cancelRequest := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.LeavePresence(requestCtx, request)
	cancelRequest()
	if err != nil {
		log.Printf("[gamecast/presence] leave failed room=%s: %v", roomID, err)
	}
}

func (a *App) gameCastShareLeaseRequest(roomID, phase, audioMode string) gamecast.ShareLeaseRequest {
	a.mu.Lock()
	participantID := a.gamecastParticipantID
	lease := a.gamecastShareLease
	presenceRoom := a.gamecastPresenceRoom
	mediaMode := a.gamecastPresenceMediaMode
	a.mu.Unlock()
	roomID = strings.TrimSpace(roomID)
	if !strings.EqualFold(strings.TrimSpace(presenceRoom), roomID) {
		mediaMode = ""
	}
	if lease != nil && strings.EqualFold(lease.Room, roomID) && strings.TrimSpace(lease.MediaMode) != "" {
		mediaMode = strings.TrimSpace(lease.MediaMode)
	}
	if mediaMode != gamecast.MediaModeLegacyP2P && mediaMode != gamecast.MediaModeSFU {
		mediaMode = gamecast.MediaModeSFU
	}
	phase = strings.TrimSpace(phase)
	if phase == "" && lease != nil {
		phase = strings.TrimSpace(lease.Phase)
	}
	if phase == "" && mediaMode == gamecast.MediaModeSFU {
		phase = gamecast.SharePhaseStarting
	}
	if mediaMode == gamecast.MediaModeLegacyP2P && phase == gamecast.SharePhaseStarting {
		phase = ""
	}
	request := gamecast.ShareLeaseRequest{
		Room:          roomID,
		ParticipantID: participantID,
		MediaMode:     mediaMode,
		Phase:         phase,
		SFU:           mediaMode == gamecast.MediaModeSFU,
		AudioMode:     strings.TrimSpace(audioMode),
		TTL:           15,
	}
	if lease != nil && strings.EqualFold(lease.Room, request.Room) {
		request.LeaseID = lease.LeaseID
	}
	return request
}

func (a *App) claimGameCastShare(roomID, audioMode string) (gamecast.ShareLease, GameCastRoomState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return gamecast.ShareLease{}, GameCastRoomState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request := a.gameCastShareLeaseRequest(roomID, gamecast.SharePhaseStarting, audioMode)
	lease, roomState, err := client.ClaimShare(requestCtx, request)
	if err != nil {
		return gamecast.ShareLease{}, GameCastRoomState{}, err
	}
	if strings.TrimSpace(lease.MediaMode) == "" {
		lease.MediaMode = request.MediaMode
	}
	if lease.MediaMode == gamecast.MediaModeLegacyP2P && strings.TrimSpace(lease.Phase) == "" {
		lease.Phase = gamecast.SharePhaseLive
	}
	lease.SFU = lease.MediaMode == gamecast.MediaModeSFU && lease.SFU
	a.mu.Lock()
	a.gamecastShareLease = &lease
	a.mu.Unlock()
	return lease, roomState, nil
}

func (a *App) heartbeatGameCastShare(roomID, phase, audioMode string) (gamecast.ShareLease, GameCastRoomState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return gamecast.ShareLease{}, GameCastRoomState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request := a.gameCastShareLeaseRequest(roomID, phase, audioMode)
	lease, roomState, err := client.HeartbeatShare(requestCtx, request)
	if err != nil {
		return gamecast.ShareLease{}, GameCastRoomState{}, err
	}
	if strings.TrimSpace(lease.MediaMode) == "" {
		lease.MediaMode = request.MediaMode
	}
	if lease.MediaMode == gamecast.MediaModeLegacyP2P && strings.TrimSpace(lease.Phase) == "" {
		lease.Phase = gamecast.SharePhaseLive
	}
	a.mu.Lock()
	a.gamecastShareLease = &lease
	a.mu.Unlock()
	return lease, roomState, nil
}

func (a *App) startGameCastShareHeartbeat(lease gamecast.ShareLease, phase, audioMode string) {
	a.mu.Lock()
	if a.gamecastShareCancel != nil {
		a.gamecastShareCancel()
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	a.gamecastShareCancel = cancel
	a.gamecastShareLease = &lease
	a.mu.Unlock()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
				if err != nil {
					log.Printf("[gamecast/share-lease] client unavailable room=%s: %v", lease.Room, err)
					continue
				}
				requestCtx, cancelRequest := context.WithTimeout(heartbeatCtx, 5*time.Second)
				request := a.gameCastShareLeaseRequest(lease.Room, phase, audioMode)
				updated, _, err := client.HeartbeatShare(requestCtx, request)
				cancelRequest()
				if err != nil {
					if heartbeatCtx.Err() == nil {
						log.Printf("[gamecast/share-lease] heartbeat failed room=%s: %v", lease.Room, err)
					}
					continue
				}
				if strings.TrimSpace(updated.MediaMode) == "" {
					updated.MediaMode = request.MediaMode
				}
				if updated.MediaMode == gamecast.MediaModeLegacyP2P && strings.TrimSpace(updated.Phase) == "" {
					updated.Phase = gamecast.SharePhaseLive
				}
				a.mu.Lock()
				a.gamecastShareLease = &updated
				a.mu.Unlock()
			}
		}
	}()
}

func (a *App) stopGameCastShareHeartbeat() {
	a.mu.Lock()
	cancel := a.gamecastShareCancel
	a.gamecastShareCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) stopGameCastScreenShare() error {
	a.stopGameCastShareHeartbeat()
	a.mu.Lock()
	lease := a.gamecastShareLease
	a.mu.Unlock()

	var transitionErr error
	if lease != nil {
		phase := strings.TrimSpace(lease.Phase)
		if lease.MediaMode == gamecast.MediaModeSFU && (phase == gamecast.SharePhaseLive || (phase == "" && lease.SFU)) {
			_, _, transitionErr = a.heartbeatGameCastShare(lease.Room, gamecast.SharePhaseStopping, lease.AudioMode)
			if isGameCastLeaseGone(transitionErr) {
				transitionErr = nil
			}
		}
	}

	a.stopGameCastNativeHost()
	a.closeGameCastWS()
	releaseErr := a.releaseGameCastShareLease()
	if isGameCastLeaseGone(releaseErr) {
		releaseErr = nil
	}
	return errors.Join(transitionErr, releaseErr)
}

func (a *App) releaseGameCastShareLease() error {
	a.stopGameCastShareHeartbeat()
	a.mu.Lock()
	lease := a.gamecastShareLease
	a.gamecastShareLease = nil
	a.mu.Unlock()
	if lease == nil || strings.TrimSpace(lease.LeaseID) == "" {
		return nil
	}
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRequest()
	_, _, err = client.ReleaseShare(requestCtx, gamecast.ShareLeaseRequest{
		Room:          lease.Room,
		ParticipantID: lease.ParticipantID,
		LeaseID:       lease.LeaseID,
		MediaMode:     lease.MediaMode,
		Phase:         lease.Phase,
		SFU:           lease.MediaMode == gamecast.MediaModeSFU,
		AudioMode:     lease.AudioMode,
		TTL:           15,
	})
	return err
}

func isGameCastLeaseGone(err error) bool {
	var apiError *gamecast.APIError
	return errors.As(err, &apiError) && apiError.Code == "share_lease_expired"
}

func (a *App) postGameCastRoomState(state GameCastRoomState) (GameCastRoomState, error) {
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return GameCastRoomState{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	posted, err := client.PostRoomState(requestCtx, state)
	if err != nil {
		return GameCastRoomState{}, err
	}
	a.rememberGameCastRoom(state.Room)
	return posted, nil
}

func (a *App) rememberGameCastRoom(roomID string) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return
	}
	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.LastRoomID = roomID
	cfg := a.cfg
	a.mu.Unlock()
	if err := config.Save(cfg); err != nil {
		log.Printf("save last gamecast room failed: %v", err)
	}
}

func (a *App) gameCastClient(profile gamecast.Profile) (*gamecast.Client, error) {
	if err := a.ensureGameCastJar(profile); err != nil {
		return nil, err
	}
	a.mu.Lock()
	jar := a.gamecastJar
	a.mu.Unlock()
	return gamecast.NewClientWithJar(profile, jar)
}

func (a *App) ensureGameCastJar(profile gamecast.Profile) error {
	a.mu.Lock()
	jar := a.gamecastJar
	a.mu.Unlock()
	if jar == nil {
		nextJar, err := session.New("")
		if err != nil {
			return err
		}
		jar = nextJar
		a.mu.Lock()
		a.gamecastJar = jar
		a.mu.Unlock()
	}
	return jar.Load(profile.BaseURL)
}

func (a *App) saveGameCastJar(profile gamecast.Profile) error {
	a.mu.Lock()
	jar := a.gamecastJar
	a.mu.Unlock()
	if jar == nil {
		return nil
	}
	return jar.Save(profile.BaseURL)
}

func (a *App) persistGameCastHistory(history []gamecast.HistoryEntry, syncRemote bool) {
	history = rooms.NormalizeHistory(history)
	a.mu.Lock()
	if a.cfg == nil {
		a.cfg = config.DefaultConfig()
	}
	a.cfg.GameCast.RoomHistory = make([]config.GameCastRoomHistoryEntry, 0, len(history))
	for _, entry := range history {
		a.cfg.GameCast.RoomHistory = append(a.cfg.GameCast.RoomHistory, config.GameCastRoomHistoryEntry{
			ID:   entry.ID,
			Time: entry.Time,
		})
	}
	cfg := a.cfg
	a.mu.Unlock()
	if err := config.Save(cfg); err != nil {
		log.Printf("save gamecast history failed: %v", err)
	}
	if !syncRemote {
		return
	}
	client, err := a.gameCastClient(a.gameCastProfileForRequest(""))
	if err != nil {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := client.SaveHistory(requestCtx, history); err != nil {
		log.Printf("sync gamecast history failed: %v", err)
	}
}

func localGameCastHistoryLocked(cfg *config.Config) []gamecast.HistoryEntry {
	if cfg == nil {
		return nil
	}
	out := make([]gamecast.HistoryEntry, 0, len(cfg.GameCast.RoomHistory))
	for _, entry := range cfg.GameCast.RoomHistory {
		out = append(out, gamecast.HistoryEntry{
			ID:   entry.ID,
			Time: entry.Time,
		})
	}
	return rooms.NormalizeHistory(out)
}

func (a *App) gameCastUserLabel() string {
	sessionState, err := a.GetGameCastSession()
	if err == nil && sessionState.User != "" && sessionState.User != "guest" {
		return sessionState.User
	}
	if name := a.configuredDisplayName(); name != "" {
		return name
	}
	return "PartyLAN"
}

func gameCastSessionStateFromConfig(cfg gamecast.ServerConfig) GameCastSessionState {
	return GameCastSessionState{
		User:     cfg.User,
		LoggedIn: cfg.LoggedIn,
		AuthMode: cfg.AuthMode,
		Version:  cfg.Version,
		RoomName: cfg.RoomName,
	}
}

func (a *App) gameCastProfileForRequest(baseURL string) gamecast.Profile {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	profile := gameCastProfileFromConfig(cfg)
	if strings.TrimSpace(baseURL) != "" {
		normalized, err := gamecast.NormalizeBaseURL(baseURL)
		if err == nil && normalized != profile.BaseURL {
			profile.BaseURL = normalized
			profile.TrustedCertificateSHA256 = ""
		} else if err == nil {
			profile.BaseURL = normalized
		}
	}
	return profile
}

func gameCastProfileFromConfig(cfg *config.Config) gamecast.Profile {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	baseURL := cfg.GameCast.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = gamecast.DefaultBaseURL
	}
	normalized, err := gamecast.NormalizeBaseURL(baseURL)
	if err != nil {
		normalized = gamecast.DefaultBaseURL
	}
	displayName := normalizeDisplayName(cfg.GameCast.DisplayName)
	if displayName == "" {
		displayName = normalizeDisplayName(cfg.DisplayName)
	}
	return gamecast.Profile{
		BaseURL:                  normalized,
		DisplayName:              displayName,
		TrustedCertificateSHA256: gamecast.NormalizeFingerprint(cfg.GameCast.TrustedCertificateSHA256),
	}
}

func gameCastProfileForUI(cfg *config.Config) GameCastProfile {
	profile := gameCastProfileFromConfig(cfg)
	return GameCastProfile{
		BaseURL:                  profile.BaseURL,
		DisplayName:              profile.DisplayName,
		TrustedCertificateSHA256: profile.TrustedCertificateSHA256,
	}
}

func normalizeGameCastLayer(layer string) string {
	switch strings.ToLower(strings.TrimSpace(layer)) {
	case "q", "h", "f", "auto":
		return strings.ToLower(strings.TrimSpace(layer))
	default:
		return "auto"
	}
}

func (a *App) emitGameCastSignal(message json.RawMessage) {
	if a.ctx == nil {
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(message, &payload); err != nil {
		log.Printf("[gamecast/ws] invalid signal payload: %v", err)
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventGameCastSignal), payload)
}

func (a *App) emitGameCastStatus(status gamecastws.Status) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, string(app.EventGameCastStatus), status)
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
		a.emitError(app.ErrCodeRoomInvalidated, "当前房间连接已失效，请重新创建或加入房间。")
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
	networkBackend := describeNetworkBackend(a.network)
	if appmode.ResolveMode() == appmode.ModeGameCastClient {
		networkBackend = string(appmode.ModeGameCastClient)
	}

	return app.RuntimeStatusEvent{
		Mode:           string(appmode.ResolveMode()),
		NetworkBackend: networkBackend,
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
