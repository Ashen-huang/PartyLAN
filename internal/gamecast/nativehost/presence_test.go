package nativehost

import (
	"encoding/json"
	"testing"
)

func TestHandleSignalForwardsPresenceToAuxCallback(t *testing.T) {
	var gotType string
	host := New(Config{
		OnAuxMessage: func(message json.RawMessage) {
			var payload struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(message, &payload); err != nil {
				t.Fatalf("unmarshal aux message: %v", err)
			}
			gotType = payload.Type
		},
	})

	if err := host.handleSignal(json.RawMessage(`{"type":"presence","viewers":[{"id":"viewer-1","name":"Ashen"}]}`)); err != nil {
		t.Fatalf("handleSignal(presence) returned error: %v", err)
	}
	if gotType != "presence" {
		t.Fatalf("aux callback type = %q, want presence", gotType)
	}
}
