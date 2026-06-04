package room

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

type MessageType byte

const (
	MsgRoomCreate   MessageType = 0x01
	MsgRoomJoin     MessageType = 0x02
	MsgRoomLeave    MessageType = 0x03
	MsgPeerAnnounce MessageType = 0x04
	MsgStreamStart  MessageType = 0x05
	MsgStreamStop   MessageType = 0x06
	MsgHeartbeat    MessageType = 0x07
	MsgVideoData    MessageType = 0x08
	MsgMediaSignal  MessageType = 0x09
	MsgError        MessageType = 0xFF
)

const headerSize = 5 // 4 bytes length + 1 byte type

type Message struct {
	Type    MessageType
	Payload []byte
}

type RoomCreatePayload struct {
	RoomID   string `json:"roomId"`
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
	Hostname string `json:"hostname"`
}

type RoomJoinPayload struct {
	PeerID          string                  `json:"peerId"`
	RoomID          string                  `json:"roomId"`
	Password        string                  `json:"password,omitempty"`
	Hostname        string                  `json:"hostname"`
	VideoCapability *VideoCapabilityPayload `json:"videoCapability,omitempty"`
}

type PeerAnnouncePayload struct {
	PeerID          string                  `json:"peerId"`
	Hostname        string                  `json:"hostname"`
	Role            int                     `json:"role"`
	VideoCapability *VideoCapabilityPayload `json:"videoCapability,omitempty"`
}

type StreamStartPayload struct {
	PeerID  string `json:"peerId"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Codec   string `json:"codec,omitempty"`
	Backend string `json:"backend,omitempty"`
}

type StreamStopPayload struct {
	PeerID string `json:"peerId"`
}

type MediaSignalPayload struct {
	SessionID  string          `json:"sessionId"`
	FromPeerID string          `json:"fromPeerId"`
	ToPeerID   string          `json:"toPeerId"`
	Kind       string          `json:"kind"`
	Media      string          `json:"media"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type HeartbeatPayload struct {
	PeerID    string `json:"peerId"`
	Timestamp int64  `json:"timestamp"`
}

type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type VideoCapabilityPayload struct {
	Backend      string `json:"backend,omitempty"`
	Status       string `json:"status,omitempty"`
	Reason       string `json:"reason,omitempty"`
	SupportsHost bool   `json:"supportsHost,omitempty"`
	SupportsView bool   `json:"supportsView,omitempty"`
}

func WriteMessage(w io.Writer, msgType MessageType, payload []byte) error {
	length := uint32(len(payload))
	header := make([]byte, headerSize)
	binary.BigEndian.PutUint32(header[0:4], length)
	header[4] = byte(msgType)

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}
	return nil
}

func ReadMessage(r io.Reader) (*Message, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	length := binary.BigEndian.Uint32(header[0:4])
	msgType := MessageType(header[4])

	if length > 10*1024*1024 {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("read payload: %w", err)
		}
	}

	return &Message{Type: msgType, Payload: payload}, nil
}

func MarshalPayload(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func UnmarshalPayload(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func WriteTypedMessage(w io.Writer, msgType MessageType, v interface{}) error {
	payload, err := MarshalPayload(v)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	return WriteMessage(w, msgType, payload)
}
