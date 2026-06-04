package room

import (
	"bytes"
	"testing"
)

func TestWriteReadMessage(t *testing.T) {
	var buf bytes.Buffer

	payload := []byte(`{"roomId":"test","name":"test-room"}`)
	err := WriteMessage(&buf, MsgRoomCreate, payload)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	msg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if msg.Type != MsgRoomCreate {
		t.Errorf("expected type %d, got %d", MsgRoomCreate, msg.Type)
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Errorf("payload mismatch")
	}
}

func TestWriteReadEmptyPayload(t *testing.T) {
	var buf bytes.Buffer

	err := WriteMessage(&buf, MsgHeartbeat, nil)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	msg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if msg.Type != MsgHeartbeat {
		t.Errorf("expected type %d, got %d", MsgHeartbeat, msg.Type)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(msg.Payload))
	}
}

func TestWriteTypedMessage(t *testing.T) {
	var buf bytes.Buffer

	payload := HeartbeatPayload{
		PeerID:    "peer-1",
		Timestamp: 1234567890,
	}
	err := WriteTypedMessage(&buf, MsgHeartbeat, payload)
	if err != nil {
		t.Fatalf("WriteTypedMessage: %v", err)
	}

	msg, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var decoded HeartbeatPayload
	if err := UnmarshalPayload(msg.Payload, &decoded); err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}

	if decoded.PeerID != "peer-1" {
		t.Errorf("expected peerId 'peer-1', got %q", decoded.PeerID)
	}
	if decoded.Timestamp != 1234567890 {
		t.Errorf("expected timestamp 1234567890, got %d", decoded.Timestamp)
	}
}

func TestMultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	messages := []struct {
		msgType MessageType
		payload interface{}
	}{
		{MsgRoomCreate, RoomCreatePayload{RoomID: "r1", Name: "Room 1"}},
		{MsgRoomJoin, RoomJoinPayload{PeerID: "peer-1", RoomID: "r1", Hostname: "host1"}},
		{MsgStreamStart, StreamStartPayload{PeerID: "p1", Width: 1920, Height: 1080, FPS: 30}},
		{MsgHeartbeat, HeartbeatPayload{PeerID: "p1", Timestamp: 999}},
		{MsgStreamStop, StreamStopPayload{PeerID: "p1"}},
	}

	for _, m := range messages {
		if err := WriteTypedMessage(&buf, m.msgType, m.payload); err != nil {
			t.Fatalf("WriteTypedMessage: %v", err)
		}
	}

	for i, m := range messages {
		msg, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		if msg.Type != m.msgType {
			t.Errorf("[%d] expected type %d, got %d", i, m.msgType, msg.Type)
		}
	}
}

func TestMessageTooLarge(t *testing.T) {
	var buf bytes.Buffer

	// Write a header claiming 20MB payload
	header := make([]byte, headerSize)
	header[0] = 0x01 // 20MB in big endian
	header[1] = 0x40
	header[2] = 0x00
	header[3] = 0x00
	header[4] = byte(MsgVideoData)
	buf.Write(header)

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("expected error for message too large")
	}
}

func TestReadFromEmptyReader(t *testing.T) {
	var buf bytes.Buffer
	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("expected error reading from empty buffer")
	}
}
