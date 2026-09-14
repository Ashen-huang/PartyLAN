package gamecast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	profile Profile
	http    *http.Client
}

type ProbeResult struct {
	OK                     bool   `json:"ok"`
	Status                 string `json:"status"`
	BaseURL                string `json:"baseUrl"`
	User                   string `json:"user,omitempty"`
	LoggedIn               bool   `json:"loggedIn"`
	RoomName               string `json:"roomName,omitempty"`
	CertificateFingerprint string `json:"certificateFingerprint,omitempty"`
	CertificateTrusted     bool   `json:"certificateTrusted"`
	Message                string `json:"message,omitempty"`
}

type ServerConfig struct {
	AuthMode string `json:"authMode"`
	User     string `json:"user"`
	LoggedIn bool   `json:"loggedIn"`
	Version  string `json:"version"`
	RoomName string `json:"roomName"`
}

type HistoryEntry struct {
	ID   string `json:"id"`
	Time int64  `json:"time"`
}

type HistoryResponse struct {
	OK      bool           `json:"ok"`
	History []HistoryEntry `json:"history"`
	Error   string         `json:"error,omitempty"`
}

type APIResponse struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("server returned status %d", e.StatusCode)
}

func NewClient(profile Profile) (*Client, error) {
	jar, _ := cookiejar.New(nil)
	return NewClientWithJar(profile, jar)
}

func NewClientWithJar(profile Profile, jar http.CookieJar) (*Client, error) {
	normalized, err := NormalizeBaseURL(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	profile.BaseURL = normalized
	profile.TrustedCertificateSHA256 = NormalizeFingerprint(profile.TrustedCertificateSHA256)

	tlsConfig, err := TLSConfig(profile)
	if err != nil {
		return nil, err
	}
	if jar == nil {
		jar, _ = cookiejar.New(nil)
	}
	return &Client{
		profile: profile,
		http: &http.Client{
			Timeout: 8 * time.Second,
			Jar:     jar,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	}, nil
}

func (c *Client) Profile() Profile {
	return c.profile
}

func (c *Client) Probe(ctx context.Context) ProbeResult {
	result := ProbeResult{
		BaseURL:            c.profile.BaseURL,
		CertificateTrusted: c.profile.TrustedCertificateSHA256 != "",
	}

	cfg, err := c.GetServerConfig(ctx)
	if err == nil {
		result.OK = true
		result.Status = "ok"
		result.User = cfg.User
		result.LoggedIn = cfg.LoggedIn
		result.RoomName = cfg.RoomName
		result.Message = "server reachable"
		return result
	}

	var certErr *UntrustedCertificateError
	if errors.As(err, &certErr) {
		result.Status = "tls_untrusted"
		result.CertificateFingerprint = certErr.Fingerprint
		result.CertificateTrusted = false
		result.Message = certErr.Error()
		return result
	}

	result.Status = "error"
	result.Message = err.Error()
	return result
}

func (c *Client) GetServerConfig(ctx context.Context) (ServerConfig, error) {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/config")
	if err != nil {
		return ServerConfig{}, err
	}

	var cfg ServerConfig
	if err := c.getJSON(ctx, endpoint, &cfg); err != nil {
		return ServerConfig{}, err
	}
	return cfg, nil
}

func (c *Client) GetHistory(ctx context.Context) ([]HistoryEntry, error) {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/history")
	if err != nil {
		return nil, err
	}

	var response HistoryResponse
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return nil, err
	}
	if !response.OK && response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return response.History, nil
}

func (c *Client) SaveHistory(ctx context.Context, history []HistoryEntry) error {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/history")
	if err != nil {
		return err
	}
	payload := struct {
		History []HistoryEntry `json:"history"`
	}{
		History: history,
	}
	var response APIResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, &response); err != nil {
		return err
	}
	if !response.OK && response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func (c *Client) PostStats(ctx context.Context, payload map[string]interface{}) error {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/stats")
	if err != nil {
		return err
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	var response APIResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, &response); err != nil {
		return err
	}
	if !response.OK && response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func (c *Client) ReportQuality(ctx context.Context, report QualityReport) error {
	if strings.TrimSpace(report.Room) == "" || strings.TrimSpace(report.ParticipantID) == "" {
		return fmt.Errorf("room and participant id are required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/quality/report")
	if err != nil {
		return err
	}
	var response APIResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, report, &response); err != nil {
		return err
	}
	if !response.OK && response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func (c *Client) GetQualitySnapshot(ctx context.Context, roomID, participantID string) (QualitySnapshot, error) {
	roomID = strings.TrimSpace(roomID)
	participantID = strings.TrimSpace(participantID)
	if roomID == "" || participantID == "" {
		return QualitySnapshot{}, fmt.Errorf("room and participant id are required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/quality/snapshot")
	if err != nil {
		return QualitySnapshot{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return QualitySnapshot{}, err
	}
	query := parsed.Query()
	query.Set("room", roomID)
	query.Set("participantId", participantID)
	parsed.RawQuery = query.Encode()

	var response QualitySnapshotResponse
	if err := c.getJSON(ctx, parsed.String(), &response); err != nil {
		return QualitySnapshot{}, err
	}
	return response.QualitySnapshot, nil
}

func (c *Client) ListRooms(ctx context.Context) ([]RoomState, error) {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/rooms")
	if err != nil {
		return nil, err
	}

	var response RoomsResponse
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return nil, err
	}
	if !response.OK && response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if response.Rooms == nil {
		return []RoomState{}, nil
	}
	return response.Rooms, nil
}

func (c *Client) Register(ctx context.Context, user, pass string) error {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/register")
	if err != nil {
		return err
	}
	payload := map[string]string{
		"user": strings.TrimSpace(user),
		"pass": pass,
	}
	var response APIResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, payload, &response); err != nil {
		return err
	}
	if !response.OK {
		if response.Error != "" {
			return errors.New(response.Error)
		}
		if response.Message != "" {
			return errors.New(response.Message)
		}
		return errors.New("register failed")
	}
	return nil
}

func (c *Client) Login(ctx context.Context, user, pass string) (ServerConfig, error) {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/login")
	if err != nil {
		return ServerConfig{}, err
	}
	form := url.Values{}
	form.Set("user", strings.TrimSpace(user))
	form.Set("pass", pass)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ServerConfig{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return ServerConfig{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var response APIResponse
		if len(body) > 0 {
			_ = json.Unmarshal(body, &response)
		}
		if response.Message != "" {
			return ServerConfig{}, errors.New(response.Message)
		}
		return ServerConfig{}, fmt.Errorf("login failed: %s", resp.Status)
	}
	return c.GetServerConfig(ctx)
}

func (c *Client) Logout(ctx context.Context) error {
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/logout")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("logout failed: %s", resp.Status)
	}
	return nil
}

func (c *Client) GetRoomState(ctx context.Context, roomID string) (RoomState, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return RoomState{}, fmt.Errorf("room id is required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/room-state")
	if err != nil {
		return RoomState{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return RoomState{}, err
	}
	query := parsed.Query()
	query.Set("room", roomID)
	parsed.RawQuery = query.Encode()

	var response RoomStateResponse
	if err := c.getJSON(ctx, parsed.String(), &response); err != nil {
		return RoomState{}, err
	}
	if response.State != nil {
		return *response.State, nil
	}
	return RoomState{
		Room:    roomID,
		Active:  false,
		Sharing: false,
		SFU:     false,
	}, nil
}

func (c *Client) PostRoomState(ctx context.Context, state RoomState) (RoomState, error) {
	if strings.TrimSpace(state.Room) == "" {
		return RoomState{}, fmt.Errorf("room id is required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/room-state")
	if err != nil {
		return RoomState{}, err
	}
	var response RoomStateResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, state, &response); err != nil {
		return RoomState{}, err
	}
	if response.Error != "" {
		return RoomState{}, errors.New(response.Error)
	}
	if response.State != nil {
		return *response.State, nil
	}
	return state, nil
}

func (c *Client) PostViewerIntent(ctx context.Context, intent ViewerIntent) error {
	if strings.TrimSpace(intent.Room) == "" {
		return fmt.Errorf("room id is required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/viewer-intent")
	if err != nil {
		return err
	}
	var response ViewerIntentResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, intent, &response); err != nil {
		return err
	}
	if !response.OK && response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

func (c *Client) JoinPresence(ctx context.Context, request PresenceRequest) (PresenceParticipant, RoomState, error) {
	response, err := c.postPresence(ctx, "/api/presence/join", request)
	return presenceResult(response, err)
}

func (c *Client) HeartbeatPresence(ctx context.Context, request PresenceRequest) (PresenceParticipant, error) {
	response, err := c.postPresence(ctx, "/api/presence/heartbeat", request)
	participant, _, err := presenceResult(response, err)
	return participant, err
}

func (c *Client) LeavePresence(ctx context.Context, request PresenceRequest) (PresenceParticipant, error) {
	response, err := c.postPresence(ctx, "/api/presence/leave", request)
	participant, _, err := presenceResult(response, err)
	return participant, err
}

func (c *Client) ListPresence(ctx context.Context, roomID string) ([]PresenceParticipant, RoomState, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, RoomState{}, fmt.Errorf("room id is required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/presence")
	if err != nil {
		return nil, RoomState{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, RoomState{}, err
	}
	query := parsed.Query()
	query.Set("room", roomID)
	parsed.RawQuery = query.Encode()
	var response PresenceResponse
	if err := c.getJSON(ctx, parsed.String(), &response); err != nil {
		return nil, RoomState{}, err
	}
	if response.Room == nil {
		return response.Participants, RoomState{Room: roomID}, nil
	}
	return response.Participants, *response.Room, nil
}

func (c *Client) IssueVoiceToken(ctx context.Context, request VoiceTokenRequest) (VoiceTokenResponse, error) {
	if strings.TrimSpace(request.Room) == "" || strings.TrimSpace(request.ParticipantID) == "" {
		return VoiceTokenResponse{}, fmt.Errorf("room and participant id are required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, "/api/voice/token")
	if err != nil {
		return VoiceTokenResponse{}, err
	}
	var response VoiceTokenResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, request, &response); err != nil {
		return VoiceTokenResponse{}, err
	}
	if strings.TrimSpace(response.Token) == "" || response.ExpiresAtUnix <= 0 {
		return VoiceTokenResponse{}, fmt.Errorf("voice token response is incomplete")
	}
	return response, nil
}

func (c *Client) ClaimShare(ctx context.Context, request ShareLeaseRequest) (ShareLease, RoomState, error) {
	response, err := c.postShareLease(ctx, "/api/share/claim", request)
	return shareLeaseResult(response, err)
}

func (c *Client) HeartbeatShare(ctx context.Context, request ShareLeaseRequest) (ShareLease, RoomState, error) {
	response, err := c.postShareLease(ctx, "/api/share/heartbeat", request)
	return shareLeaseResult(response, err)
}

func (c *Client) ReleaseShare(ctx context.Context, request ShareLeaseRequest) (ShareLease, RoomState, error) {
	response, err := c.postShareLease(ctx, "/api/share/release", request)
	return shareLeaseResult(response, err)
}

func (c *Client) postPresence(ctx context.Context, path string, request PresenceRequest) (PresenceResponse, error) {
	if strings.TrimSpace(request.Room) == "" || strings.TrimSpace(request.ParticipantID) == "" {
		return PresenceResponse{}, fmt.Errorf("room and participant id are required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, path)
	if err != nil {
		return PresenceResponse{}, err
	}
	var response PresenceResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, request, &response); err != nil {
		return PresenceResponse{}, err
	}
	return response, nil
}

func presenceResult(response PresenceResponse, err error) (PresenceParticipant, RoomState, error) {
	if err != nil {
		return PresenceParticipant{}, RoomState{}, err
	}
	if response.Participant == nil {
		return PresenceParticipant{}, RoomState{}, fmt.Errorf("presence response omitted participant")
	}
	room := RoomState{}
	if response.Room != nil {
		room = *response.Room
	}
	return *response.Participant, room, nil
}

func (c *Client) postShareLease(ctx context.Context, path string, request ShareLeaseRequest) (ShareLeaseResponse, error) {
	if strings.TrimSpace(request.Room) == "" || strings.TrimSpace(request.ParticipantID) == "" {
		return ShareLeaseResponse{}, fmt.Errorf("room and participant id are required")
	}
	endpoint, err := URLForEndpoint(c.profile.BaseURL, path)
	if err != nil {
		return ShareLeaseResponse{}, err
	}
	var response ShareLeaseResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, request, &response); err != nil {
		return ShareLeaseResponse{}, err
	}
	return response, nil
}

func shareLeaseResult(response ShareLeaseResponse, err error) (ShareLease, RoomState, error) {
	if err != nil {
		return ShareLease{}, RoomState{}, err
	}
	if response.Lease == nil {
		return ShareLease{}, RoomState{}, fmt.Errorf("share response omitted lease")
	}
	room := RoomState{}
	if response.Room != nil {
		room = *response.Room
	}
	return *response.Lease, room, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(resp.Body)
		return decodeAPIError(resp.StatusCode, responseBody)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, payload interface{}, target interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(resp.Body)
		return decodeAPIError(resp.StatusCode, responseBody)
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func decodeAPIError(statusCode int, body []byte) error {
	var response APIResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &response)
	}
	code := strings.TrimSpace(response.Code)
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = strings.TrimSpace(response.Error)
	}
	if code == "" && message != "" {
		code = message
	}
	return &APIError{StatusCode: statusCode, Code: code, Message: message}
}
