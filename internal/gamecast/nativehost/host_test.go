package nativehost

import (
	"context"
	"strings"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

func TestH264LevelIDHex(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		fps    int
		want   string
	}{
		{name: "720p30", width: 1280, height: 720, fps: 30, want: "1f"},
		{name: "1080p30", width: 1920, height: 1080, fps: 30, want: "28"},
		{name: "1080p60", width: 1920, height: 1080, fps: 60, want: "2a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := h264LevelIDHex(tt.width, tt.height, tt.fps); got != tt.want {
				t.Fatalf("h264LevelIDHex(%d,%d,%d) = %q, want %q", tt.width, tt.height, tt.fps, got, tt.want)
			}
		})
	}
}

func TestH264SDPFmtpLineUsesComputedLevel(t *testing.T) {
	got := h264SDPFmtpLine(1920, 1080, 30)
	want := "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e028"
	if got != want {
		t.Fatalf("h264SDPFmtpLine(1920,1080,30) = %q, want %q", got, want)
	}
}

func TestSingleVideoLayerUsesConfiguredSourceSize(t *testing.T) {
	layers := SingleVideoLayer(1920, 1080, 30)
	if len(layers) != 1 {
		t.Fatalf("single video layer count = %d, want 1", len(layers))
	}
	got := layers[0]
	if got.RID != "f" || got.Width != 1920 || got.Height != 1080 || got.FPS != 30 {
		t.Fatalf("single video layer = %#v, want f 1920x1080@30", got)
	}
}

func TestSingleVideoLayerKeepsDimensionsEvenAndPositive(t *testing.T) {
	layers := SingleVideoLayer(1279, 719, 0)
	if len(layers) != 1 {
		t.Fatalf("single video layer count = %d, want 1", len(layers))
	}
	got := layers[0]
	if got.Width%2 != 0 || got.Height%2 != 0 || got.Width <= 0 || got.Height <= 0 {
		t.Fatalf("single video layer dimensions must be positive and even: %#v", got)
	}
	if got.FPS != 30 {
		t.Fatalf("single video layer fps = %d, want default 30", got.FPS)
	}
}

func TestSingleVideoLayerUsesAdaptiveLadderBitrates(t *testing.T) {
	tests := []struct {
		width, height, fps, want int
	}{
		{1920, 1080, 60, 8000},
		{1920, 1080, 45, 7000},
		{1920, 1080, 30, 6000},
		{1280, 720, 30, 3500},
		{1280, 720, 24, 3000},
		{960, 540, 24, 1700},
		{640, 360, 24, 900},
		{640, 360, 18, 600},
	}
	for _, tt := range tests {
		got := SingleVideoLayer(tt.width, tt.height, tt.fps)[0].BitrateKbps
		if got != tt.want {
			t.Fatalf("%dx%d@%d bitrate = %d, want %d", tt.width, tt.height, tt.fps, got, tt.want)
		}
	}
}

func TestActiveVideoLayersKeepsSingleTrackForLegacyLayerRequests(t *testing.T) {
	host := New(Config{})
	host.videoLayers = SingleVideoLayer(1280, 720, 30)
	host.presenceReceived = true
	host.videoLayerViewers = map[string]int{"q": 1}

	active := host.ActiveVideoLayers()
	if len(active) != 1 || active[0].RID != "f" {
		t.Fatalf("active video layers = %#v, want the single f track", active)
	}
}

func TestCreatePeerConnectionUsesSingleVideoTrackAndAddsAudio(t *testing.T) {
	host := New(Config{
		Profile:      gamecast.Profile{BaseURL: gamecast.DefaultBaseURL},
		Width:        1280,
		Height:       720,
		FPS:          30,
		AudioEnabled: true,
	})

	pc, videoTracks, _, audioTrack, err := host.createPeerConnection(context.Background(), nil)
	if err != nil {
		t.Fatalf("createPeerConnection: %v", err)
	}
	defer pc.Close()

	if len(videoTracks) != 1 {
		t.Fatalf("video track count = %d, want 1", len(videoTracks))
	}
	videoTrack := videoTracks["f"]
	if videoTrack == nil {
		t.Fatal("expected video track")
	}
	if videoTrack.RID() != "" {
		t.Fatalf("single video track RID = %q, want empty", videoTrack.RID())
	}
	videoSenders := 0
	for _, sender := range pc.GetSenders() {
		if sender.Track() != nil && sender.Track().Kind() == webrtc.RTPCodecTypeVideo {
			videoSenders++
			if got := len(sender.GetParameters().Encodings); got != 1 {
				t.Fatalf("video sender encodings = %d, want 1", got)
			}
		}
	}
	if videoSenders != 1 {
		t.Fatalf("video sender count = %d, want 1", videoSenders)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	for _, value := range []string{"a=rid:", "a=simulcast:"} {
		if strings.Contains(offer.SDP, value) {
			t.Fatalf("offer SDP unexpectedly contains %q:\n%s", value, offer.SDP)
		}
	}
	if audioTrack == nil {
		t.Fatal("expected audio track when audio is enabled")
	}
}

func TestSingleVideoTrackProducesRemoteOnTrackAndRTP(t *testing.T) {
	layer := SingleVideoLayer(1280, 720, 30)[0]
	videoTrack, err := newSingleH264VideoTrack(layer, "test-stream")
	if err != nil {
		t.Fatalf("newSingleH264VideoTrack: %v", err)
	}

	senderPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create sender peer: %v", err)
	}
	defer senderPC.Close()
	sender, err := senderPC.AddTrack(videoTrack)
	if err != nil {
		t.Fatalf("add video track: %v", err)
	}
	defer sender.Stop()
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, readErr := sender.Read(buffer); readErr != nil {
				return
			}
		}
	}()

	receiverPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create receiver peer: %v", err)
	}
	defer receiverPC.Close()
	receivedTrack := make(chan *webrtc.TrackRemote, 1)
	receivedRTP := make(chan struct{}, 1)
	receiverPC.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		receivedTrack <- track
		if _, _, readErr := track.ReadRTP(); readErr == nil {
			receivedRTP <- struct{}{}
		}
	})

	offer, err := senderPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := senderPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("set sender local description: %v", err)
	}
	<-webrtc.GatheringCompletePromise(senderPC)
	if err := receiverPC.SetRemoteDescription(*senderPC.LocalDescription()); err != nil {
		t.Fatalf("set receiver remote description: %v", err)
	}
	answer, err := receiverPC.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	if err := receiverPC.SetLocalDescription(answer); err != nil {
		t.Fatalf("set receiver local description: %v", err)
	}
	<-webrtc.GatheringCompletePromise(receiverPC)
	if err := senderPC.SetRemoteDescription(*receiverPC.LocalDescription()); err != nil {
		t.Fatalf("set sender remote description: %v", err)
	}

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = videoTrack.WriteSample(pionmedia.Sample{
				Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84},
				Duration: time.Second / 30,
			})
		case track := <-receivedTrack:
			if track.Kind() != webrtc.RTPCodecTypeVideo {
				t.Fatalf("remote track kind = %s, want video", track.Kind())
			}
			if track.RID() != "" {
				t.Fatalf("remote track RID = %q, want empty", track.RID())
			}
			select {
			case <-receivedRTP:
				return
			case <-time.After(2 * time.Second):
				t.Fatal("remote video track did not receive RTP")
			}
		case <-deadline:
			t.Fatal("remote peer did not receive video track")
		}
	}
}
