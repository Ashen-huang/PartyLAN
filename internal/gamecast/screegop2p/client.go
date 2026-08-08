package screegop2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"PartyLAN/internal/gamecast"
	gamecastws "PartyLAN/internal/gamecast/ws"

	"github.com/gorilla/websocket"
)

type Options struct {
	Profile   gamecast.Profile
	Jar       http.CookieJar
	RoomID    string
	Name      string
	Owner     bool
	OnMessage func(IncomingMessage)
	OnStatus  func(gamecastws.Status)
}

type Client struct {
	options Options

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
	cancel  context.CancelFunc
	closed  bool
	room    RoomPayload
}

func NewClient(options Options) *Client {
	return &Client{options: options}
}

func (c *Client) Connect(parent context.Context) error {
	roomID := strings.TrimSpace(c.options.RoomID)
	if roomID == "" {
		return &ProtocolError{Code: ErrorProtocol, detail: "room id is required"}
	}
	if parent == nil {
		parent = context.Background()
	}
	wsURL, err := StreamURL(c.options.Profile.BaseURL)
	if err != nil {
		return err
	}
	tlsConfig, err := gamecast.TLSConfig(c.options.Profile)
	if err != nil {
		return err
	}
	header, err := c.cookieHeader()
	if err != nil {
		return err
	}

	connectCtx, cancelConnect := context.WithTimeout(parent, 8*time.Second)
	defer cancelConnect()
	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second, TLSClientConfig: tlsConfig}
	c.emit(gamecastws.Status{State: "connecting", Detail: "screego stream"})
	conn, _, err := dialer.DialContext(connectCtx, wsURL, header)
	if err != nil {
		c.emit(gamecastws.Status{State: "error", Detail: ErrorUnavailable})
		return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
	}

	joinMessage := OutgoingMessage{
		Type: MessageJoin,
		Payload: JoinPayload{
			ID: roomID, Username: strings.TrimSpace(c.options.Name),
		},
	}
	if c.options.Owner {
		joinMessage = OutgoingMessage{
			Type: MessageCreate,
			Payload: CreatePayload{
				ID: roomID, Mode: RoomModeTURN, CloseOnOwnerLeave: true,
				Username: strings.TrimSpace(c.options.Name), JoinIfExist: true,
			},
		}
	}
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
	}
	if err := conn.WriteJSON(joinMessage); err != nil {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
	}
	_ = conn.SetWriteDeadline(time.Time{})
	if err := conn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
	}
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
	}
	if messageType != websocket.TextMessage {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorProtocol, detail: "first message is not text"}
	}
	message, err := DecodeIncoming(raw)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if serverError := message.ServerError(); serverError != nil {
		_ = conn.Close()
		return serverError
	}
	room, err := message.Room()
	if err != nil || !strings.EqualFold(strings.TrimSpace(room.ID), roomID) {
		_ = conn.Close()
		return &ProtocolError{Code: ErrorProtocol, detail: "room acknowledgement mismatch"}
	}
	_ = conn.SetReadDeadline(time.Time{})

	runCtx, runCancel := context.WithCancel(parent)
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		runCancel()
		_ = conn.Close()
		return &ProtocolError{Code: ErrorProtocol, detail: "client is already connected"}
	}
	c.conn = conn
	c.cancel = runCancel
	c.closed = false
	c.room = room
	c.mu.Unlock()

	c.emit(gamecastws.Status{State: "connected", Detail: "screego room joined"})
	if c.options.OnMessage != nil {
		c.options.OnMessage(message)
	}
	go c.readLoop(runCtx, conn)
	return nil
}

func (c *Client) Send(ctx context.Context, message OutgoingMessage) error {
	if strings.TrimSpace(message.Type) == "" || message.Payload == nil {
		return &ProtocolError{Code: ErrorProtocol, detail: "outgoing message is incomplete"}
	}
	body, err := json.Marshal(message)
	if err != nil {
		return &ProtocolError{Code: ErrorProtocol, detail: err.Error()}
	}
	return c.SendRaw(ctx, body)
}

func (c *Client) SendRaw(ctx context.Context, body []byte) error {
	if !json.Valid(body) {
		return &ProtocolError{Code: ErrorProtocol, detail: "outgoing message is invalid JSON"}
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return &ProtocolError{Code: ErrorUnavailable, detail: "screego stream is not connected"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := conn.WriteMessage(websocket.TextMessage, body)
		_ = conn.SetWriteDeadline(time.Time{})
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return &ProtocolError{Code: ErrorUnavailable, detail: err.Error()}
		}
		return nil
	}
}

func (c *Client) Room() RoomPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.room
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed && c.conn == nil {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cancel := c.cancel
	conn := c.conn
	c.cancel = nil
	c.conn = nil
	c.room = RoomPayload{}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closed"), time.Now().Add(time.Second))
		if err := conn.Close(); err != nil {
			return err
		}
	}
	c.emit(gamecastws.Status{State: "closed", Detail: "screego room closed"})
	return nil
}

func (c *Client) cookieHeader() (http.Header, error) {
	header := http.Header{}
	if c.options.Jar == nil {
		return header, nil
	}
	normalized, err := gamecast.NormalizeBaseURL(c.options.Profile.BaseURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	cookies := c.options.Jar.Cookies(parsed)
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		if value := cookie.String(); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) > 0 {
		header.Set("Cookie", strings.Join(parts, "; "))
	}
	return header, nil
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
			c.emit(gamecastws.Status{State: "closed", Detail: ErrorUnavailable})
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[gamecast/screego] stream read failed")
			}
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		message, err := DecodeIncoming(raw)
		if err != nil {
			c.emit(gamecastws.Status{State: "error", Detail: ErrorProtocol})
			continue
		}
		if serverError := message.ServerError(); serverError != nil {
			c.emit(gamecastws.Status{State: "error", Detail: serverError.Code})
			continue
		}
		if message.Type == MessageRoom {
			room, roomErr := message.Room()
			if roomErr != nil {
				c.emit(gamecastws.Status{State: "error", Detail: ErrorProtocol})
				continue
			}
			c.mu.Lock()
			c.room = room
			c.mu.Unlock()
		}
		if c.options.OnMessage != nil {
			c.options.OnMessage(message)
		}
	}
}

func (c *Client) emit(status gamecastws.Status) {
	if c.options.OnStatus != nil {
		c.options.OnStatus(status)
	}
}

func (c *Client) String() string {
	return fmt.Sprintf("screego room %s", strings.TrimSpace(c.options.RoomID))
}
