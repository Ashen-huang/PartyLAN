package screegop2p

import (
	"encoding/json"
	"testing"
)

func TestStreamURLUsesNativeScreegoEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "https", baseURL: "https://partylanapp.com", want: "wss://partylanapp.com/stream"},
		{name: "http with path", baseURL: "http://127.0.0.1:5050/base/?old=1", want: "ws://127.0.0.1:5050/base/stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StreamURL(tt.baseURL)
			if err != nil {
				t.Fatalf("StreamURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("StreamURL(%q)=%q want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestProtocolMessagesMatchScreegoJSONContract(t *testing.T) {
	tests := []struct {
		name    string
		message OutgoingMessage
		want    string
	}{
		{
			name: "create",
			message: OutgoingMessage{Type: MessageCreate, Payload: CreatePayload{
				ID: "test", JoinIfExist: true, CloseOnOwnerLeave: true, Mode: RoomModeTURN, Username: "owner",
			}},
			want: `{"type":"create","payload":{"id":"test","mode":"turn","closeOnOwnerLeave":true,"username":"owner","joinIfExist":true}}`,
		},
		{
			name:    "join",
			message: OutgoingMessage{Type: MessageJoin, Payload: JoinPayload{ID: "test", Username: "viewer"}},
			want:    `{"type":"join","payload":{"id":"test","username":"viewer"}}`,
		},
		{
			name:    "share",
			message: OutgoingMessage{Type: MessageShare, Payload: struct{}{}},
			want:    `{"type":"share","payload":{}}`,
		},
		{
			name:    "host ice",
			message: OutgoingMessage{Type: MessageHostICE, Payload: P2PPayload{SID: "session-1", Value: json.RawMessage(`{"candidate":"candidate:1"}`)}},
			want:    `{"type":"hostice","payload":{"sid":"session-1","value":{"candidate":"candidate:1"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.message)
			if err != nil {
				t.Fatalf("marshal message: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("message=%s want %s", got, tt.want)
			}
		})
	}
}

func TestDecodeIncomingSessionPreservesTemporaryICEServers(t *testing.T) {
	raw := []byte(`{"type":"clientsession","payload":{"id":"session-1","peer":"peer-1","iceServers":[{"urls":["turn:8.134.126.54:3478"],"credential":"temporary-password","username":"temporary-user"}]}}`)
	message, err := DecodeIncoming(raw)
	if err != nil {
		t.Fatalf("DecodeIncoming: %v", err)
	}
	if message.Type != MessageClientSession {
		t.Fatalf("type=%q", message.Type)
	}
	session, err := message.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if session.ID != "session-1" || session.Peer != "peer-1" {
		t.Fatalf("session=%+v", session)
	}
	if len(session.ICEServers) != 1 || session.ICEServers[0].Username != "temporary-user" || session.ICEServers[0].Credential != "temporary-password" {
		t.Fatalf("ice servers=%+v", session.ICEServers)
	}
}

func TestServerErrorsAreReducedToStableCodes(t *testing.T) {
	tests := []struct {
		message string
		code    string
	}{
		{message: "you need to login", code: ErrorAuthRequired},
		{message: "room with id absent does not exist", code: ErrorRoomNotFound},
		{message: "unexpected protocol state", code: ErrorProtocol},
	}
	for _, tt := range tests {
		err := NormalizeServerError(tt.message)
		if err.Code != tt.code {
			t.Fatalf("NormalizeServerError(%q).Code=%q want %q", tt.message, err.Code, tt.code)
		}
		if err.Error() != tt.code {
			t.Fatalf("error leaked detail: %q", err.Error())
		}
	}
}
