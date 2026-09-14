package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/gamecast"

	"github.com/gorilla/websocket"
)

type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type Options struct {
	Profile   gamecast.Profile
	RoomID    string
	Role      string
	Layer     string
	Name      string
	OnMessage func(json.RawMessage)
	OnStatus  func(Status)
}

type Client struct {
	options Options

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
	cancel  context.CancelFunc
	closed  bool
}

func NewClient(options Options) *Client {
	return &Client{
		options: options,
	}
}

func (c *Client) Connect(parent context.Context) error {
	roomID := strings.TrimSpace(c.options.RoomID)
	role := strings.TrimSpace(c.options.Role)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	if role == "" {
		role = "viewer"
	}

	wsURL, err := gamecast.WSURLWithName(c.options.Profile.BaseURL, roomID, role, c.options.Layer, c.options.Name)
	if err != nil {
		return err
	}
	tlsConfig, err := gamecast.TLSConfig(c.options.Profile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()

	dialer := websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
		TLSClientConfig:  tlsConfig,
	}
	c.emit(Status{State: "connecting", Detail: wsURL})
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		c.emit(Status{State: "error", Detail: err.Error()})
		return fmt.Errorf("connect gamecast sfu: %w", err)
	}

	runCtx, runCancel := context.WithCancel(parent)
	c.mu.Lock()
	c.conn = conn
	c.cancel = runCancel
	c.closed = false
	c.mu.Unlock()

	c.emit(Status{State: "connected", Detail: wsURL})
	go c.readLoop(runCtx, conn)
	return nil
}

func (c *Client) Send(ctx context.Context, payload []byte) error {
	if !json.Valid(payload) {
		return fmt.Errorf("gamecast signal must be valid JSON")
	}

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("gamecast sfu is not connected")
	}

	done := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		done <- conn.WriteMessage(websocket.TextMessage, payload)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("send gamecast signal: %w", err)
		}
		return nil
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cancel := c.cancel
	conn := c.conn
	c.cancel = nil
	c.conn = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closed"),
			time.Now().Add(time.Second),
		)
		if err := conn.Close(); err != nil {
			return err
		}
	}
	c.emit(Status{State: "closed", Detail: "client closed"})
	return nil
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) {
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		closed := c.closed
		c.mu.Unlock()
		if !closed {
			c.emit(Status{State: "closed", Detail: "server closed connection"})
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[gamecast/ws] read failed: %v", err)
			}
			return
		}
		if messageType != websocket.TextMessage || !json.Valid(payload) {
			continue
		}
		if c.options.OnMessage != nil {
			c.options.OnMessage(json.RawMessage(append([]byte(nil), payload...)))
		}
	}
}

func (c *Client) emit(status Status) {
	if c.options.OnStatus != nil {
		c.options.OnStatus(status)
	}
}
