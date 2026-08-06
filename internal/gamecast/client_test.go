package gamecast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveGameCastProbe(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("PARTYLAN_LIVE_GAMECAST_URL"))
	if baseURL == "" {
		t.Skip("set PARTYLAN_LIVE_GAMECAST_URL to run the live GameCast probe")
	}
	client, err := NewClient(Profile{BaseURL: baseURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	result := client.Probe(ctx)
	if !result.OK {
		t.Fatalf("live probe status=%s base=%s message=%s", result.Status, result.BaseURL, result.Message)
	}
}

func TestGetRoomState(t *testing.T) {
	var gotRoom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/room-state" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		gotRoom = r.URL.Query().Get("room")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoomStateResponse{
			OK: true,
			State: &RoomState{
				Room:      gotRoom,
				Active:    true,
				Sharing:   true,
				SFU:       true,
				AudioMode: "app",
				User:      "Ashen",
				TTL:       15,
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	state, err := client.GetRoomState(context.Background(), "room-123")
	if err != nil {
		t.Fatalf("GetRoomState: %v", err)
	}
	if gotRoom != "room-123" {
		t.Fatalf("room query=%q", gotRoom)
	}
	if !state.Active || !state.Sharing || !state.SFU || state.User != "Ashen" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestGetRoomStateDecodesCrossClientMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"state":{"room":"room-123","active":true,"origin":"client","ownerParticipantId":"owner-1","ownerName":"Desktop","sharing":true,"shareOwnerParticipantId":"web-1","shareOwnerName":"Browser","shareOrigin":"web","sfu":true,"participantCount":2}}`)
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	state, err := client.GetRoomState(context.Background(), "room-123")
	if err != nil {
		t.Fatalf("GetRoomState: %v", err)
	}
	if state.Origin != "client" || state.OwnerParticipantID != "owner-1" || state.OwnerName != "Desktop" {
		t.Fatalf("room ownership metadata = %#v", state)
	}
	if state.ShareOwnerParticipantID != "web-1" || state.ShareOwnerName != "Browser" || state.ShareOrigin != "web" {
		t.Fatalf("share ownership metadata = %#v", state)
	}
	if state.ParticipantCount != 2 {
		t.Fatalf("participant count = %d", state.ParticipantCount)
	}
}

func TestPresenceAndShareLeaseAPIs(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presence/join", "/api/presence/heartbeat", "/api/presence/leave":
			var payload PresenceRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode presence: %v", err)
			}
			if payload.Room != "room-1" || payload.ParticipantID != "client-1" || payload.ClientType != "client" {
				t.Fatalf("presence payload = %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(PresenceResponse{
				OK: true,
				Participant: &PresenceParticipant{
					ParticipantID: "client-1",
					Room:          "room-1",
					Name:          "Desktop",
					ClientType:    "client",
					Role:          "owner",
					Active:        r.URL.Path != "/api/presence/leave",
				},
				Room: &RoomState{Room: "room-1", Active: true, Origin: "client"},
			})
		case "/api/presence":
			if r.URL.Query().Get("room") != "room-1" {
				t.Fatalf("presence room query = %q", r.URL.Query().Get("room"))
			}
			_ = json.NewEncoder(w).Encode(PresenceResponse{
				OK:           true,
				Participants: []PresenceParticipant{{ParticipantID: "web-1", Room: "room-1", ClientType: "web"}},
				Room:         &RoomState{Room: "room-1", Active: true, Origin: "client"},
			})
		case "/api/share/claim", "/api/share/heartbeat", "/api/share/release":
			var payload ShareLeaseRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode lease: %v", err)
			}
			if payload.Room != "room-1" || payload.ParticipantID != "client-1" {
				t.Fatalf("lease payload = %#v", payload)
			}
			if r.URL.Path != "/api/share/claim" && payload.LeaseID != "lease-1" {
				t.Fatalf("lease id = %q", payload.LeaseID)
			}
			_ = json.NewEncoder(w).Encode(ShareLeaseResponse{
				OK:    true,
				Lease: &ShareLease{Room: "room-1", LeaseID: "lease-1", ParticipantID: "client-1", Active: r.URL.Path != "/api/share/release"},
				Room:  &RoomState{Room: "room-1", Active: true, Origin: "client"},
			})
		case "/api/voice/token":
			var payload VoiceTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode voice token request: %v", err)
			}
			if payload.Room != "room-1" || payload.ParticipantID != "client-1" {
				t.Fatalf("voice token payload = %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(VoiceTokenResponse{
				OK:            true,
				Token:         "signed-token",
				ExpiresAtUnix: 1015,
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	presence := PresenceRequest{Room: "room-1", ParticipantID: "client-1", ClientType: "client", Origin: "client", Role: "owner", Name: "Desktop", TTL: 15}
	if _, _, err := client.JoinPresence(ctx, presence); err != nil {
		t.Fatalf("JoinPresence: %v", err)
	}
	if _, err := client.HeartbeatPresence(ctx, presence); err != nil {
		t.Fatalf("HeartbeatPresence: %v", err)
	}
	participants, state, err := client.ListPresence(ctx, "room-1")
	if err != nil || len(participants) != 1 || state.Origin != "client" {
		t.Fatalf("ListPresence participants=%#v state=%#v err=%v", participants, state, err)
	}
	leaseRequest := ShareLeaseRequest{Room: "room-1", ParticipantID: "client-1", SFU: true, AudioMode: "system", TTL: 15}
	lease, _, err := client.ClaimShare(ctx, leaseRequest)
	if err != nil || lease.LeaseID != "lease-1" {
		t.Fatalf("ClaimShare lease=%#v err=%v", lease, err)
	}
	leaseRequest.LeaseID = lease.LeaseID
	if _, _, err := client.HeartbeatShare(ctx, leaseRequest); err != nil {
		t.Fatalf("HeartbeatShare: %v", err)
	}
	if _, _, err := client.ReleaseShare(ctx, leaseRequest); err != nil {
		t.Fatalf("ReleaseShare: %v", err)
	}
	voiceToken, err := client.IssueVoiceToken(ctx, VoiceTokenRequest{Room: "room-1", ParticipantID: "client-1"})
	if err != nil || voiceToken.Token != "signed-token" || voiceToken.ExpiresAtUnix != 1015 {
		t.Fatalf("IssueVoiceToken token=%#v err=%v", voiceToken, err)
	}
	if _, err := client.LeavePresence(ctx, presence); err != nil {
		t.Fatalf("LeavePresence: %v", err)
	}
	want := []string{
		"/api/presence/join",
		"/api/presence/heartbeat",
		"/api/presence",
		"/api/share/claim",
		"/api/share/heartbeat",
		"/api/share/release",
		"/api/voice/token",
		"/api/presence/leave",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths=%v want=%v", paths, want)
	}
}

func TestShareConflictPreservesStructuredErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"ok":false,"code":"share_locked","error":"share_locked"}`)
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, _, err = client.ClaimShare(context.Background(), ShareLeaseRequest{Room: "room-1", ParticipantID: "client-1"})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "share_locked" || apiError.StatusCode != http.StatusConflict {
		t.Fatalf("error=%#v", err)
	}
}

func TestProbeReportsServerConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ServerConfig{
			User:     "Ashen",
			LoggedIn: true,
			RoomName: "room-123",
		})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	probe := client.Probe(context.Background())
	if !probe.OK || probe.Status != "ok" || probe.User != "Ashen" || probe.RoomName != "room-123" {
		t.Fatalf("unexpected probe: %#v", probe)
	}
}

func TestLoginPostsScreegoFormAndFetchesConfig(t *testing.T) {
	var sawLogin bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			sawLogin = true
			if r.Method != http.MethodPost {
				t.Fatalf("login method=%s", r.Method)
			}
			if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				t.Fatalf("login content-type=%q", r.Header.Get("Content-Type"))
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("user") != "Ashen" || r.Form.Get("pass") != "secret" {
				t.Fatalf("login form=%#v", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "screego", Value: "session", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/config":
			if !sawLogin {
				t.Fatal("config requested before login")
			}
			if got, _ := r.Cookie("screego"); got == nil || got.Value != "session" {
				t.Fatalf("missing session cookie: %#v", r.Cookies())
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ServerConfig{
				AuthMode: "turn",
				User:     "Ashen",
				LoggedIn: true,
				Version:  "v1",
				RoomName: "room-123",
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cfg, err := client.Login(context.Background(), " Ashen ", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if cfg.User != "Ashen" || !cfg.LoggedIn || cfg.AuthMode != "turn" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestRegisterPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/register" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("register method=%s", r.Method)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if payload["user"] != "Ashen" || payload["pass"] != "secret" {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse{OK: true})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Register(context.Background(), " Ashen ", "secret"); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestSaveHistoryPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/history" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("history method=%s", r.Method)
		}
		var payload struct {
			History []HistoryEntry `json:"history"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(payload.History) != 1 || payload.History[0].ID != "room-1" || payload.History[0].Time != 42 {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse{OK: true})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.SaveHistory(context.Background(), []HistoryEntry{{ID: "room-1", Time: 42}}); err != nil {
		t.Fatalf("SaveHistory: %v", err)
	}
}

func TestListRooms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rooms" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("rooms method=%s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoomsResponse{
			OK:    true,
			Count: 2,
			Rooms: []RoomState{
				{Room: "Ashen", Active: true, Sharing: true, SFU: true, User: "Ashen"},
				{Room: "nina", Active: true, Sharing: false, SFU: false, User: "nina"},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	rooms, err := client.ListRooms(context.Background())
	if err != nil {
		t.Fatalf("ListRooms: %v", err)
	}
	if len(rooms) != 2 || rooms[0].Room != "Ashen" || !rooms[0].Sharing || rooms[1].Room != "nina" {
		t.Fatalf("rooms=%#v", rooms)
	}
}

func TestPostStatsPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stats" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("stats method=%s", r.Method)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if payload["room"] != "room-1" || payload["role"] != "viewer" {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse{OK: true})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.PostStats(context.Background(), map[string]interface{}{
		"room": "room-1",
		"role": "viewer",
	}); err != nil {
		t.Fatalf("PostStats: %v", err)
	}
}

func TestQualityReportAndSnapshotAPIs(t *testing.T) {
	var sawReport bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/quality/report":
			sawReport = true
			var payload QualityReport
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode quality report: %v", err)
			}
			if payload.Room != "room-1" || payload.ParticipantID != "viewer-1" || payload.PacketsLostDelta != 7 {
				t.Fatalf("quality report = %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(APIResponse{OK: true})
		case "/api/quality/snapshot":
			if !sawReport {
				t.Fatal("snapshot requested before report")
			}
			if r.URL.Query().Get("room") != "room-1" || r.URL.Query().Get("participantId") != "owner-1" {
				t.Fatalf("snapshot query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(QualitySnapshotResponse{
				OK: true,
				QualitySnapshot: QualitySnapshot{
					Room: "room-1", Count: 1, SafeVideoEgressKbps: 12000,
					Samples: []QualitySample{{QualityReport: QualityReport{ParticipantID: "viewer-1", DecodedFPS: 24}}},
				},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	report := QualityReport{
		Room: "room-1", ParticipantID: "viewer-1", Generation: "g1", PairID: "p1",
		PacketsReceivedDelta: 100, PacketsLostDelta: 7, FramesDroppedDelta: 2,
		DecodedFPS: 18, IncomingBitrateKbps: 1200, JitterMS: 130,
	}
	if err := client.ReportQuality(context.Background(), report); err != nil {
		t.Fatalf("ReportQuality: %v", err)
	}
	snapshot, err := client.GetQualitySnapshot(context.Background(), "room-1", "owner-1")
	if err != nil {
		t.Fatalf("GetQualitySnapshot: %v", err)
	}
	if snapshot.Count != 1 || snapshot.SafeVideoEgressKbps != 12000 || snapshot.Samples[0].ParticipantID != "viewer-1" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPostRoomStatePostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/room-state" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("room-state method=%s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err == nil && values.Get("room") != "" {
			t.Fatalf("room-state should be JSON, got form body=%q", string(body))
		}
		var payload RoomState
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Unmarshal: %v body=%q", err, string(body))
		}
		if payload.Room != "room-1" || !payload.Active || payload.AudioMode != "off" || payload.TTL != 15 {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RoomStateResponse{OK: true, State: &payload})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	state, err := client.PostRoomState(context.Background(), RoomState{
		Room:      "room-1",
		Active:    true,
		AudioMode: "off",
		TTL:       15,
	})
	if err != nil {
		t.Fatalf("PostRoomState: %v", err)
	}
	if state.Room != "room-1" || !state.Active {
		t.Fatalf("state=%#v", state)
	}
}

func TestPostViewerIntentPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/viewer-intent" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("viewer-intent method=%s", r.Method)
		}
		var payload ViewerIntent
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if payload.Room != "room-1" || !payload.Active || !payload.WantsSFU || payload.Client != "partylan" {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ViewerIntentResponse{OK: true, Intent: &payload})
	}))
	defer server.Close()

	client, err := NewClient(Profile{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.PostViewerIntent(context.Background(), ViewerIntent{
		Room:     "room-1",
		Active:   true,
		WantsSFU: true,
		Client:   "partylan",
		TTL:      15,
	}); err != nil {
		t.Fatalf("PostViewerIntent: %v", err)
	}
}
