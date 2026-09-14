package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("voice token invalid")
	ErrExpiredToken = errors.New("voice token expired")
)

type Claims struct {
	Audience      string `json:"aud"`
	Room          string `json:"room"`
	ParticipantID string `json:"participantId"`
	ClientType    string `json:"clientType"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
	TokenID       string `json:"jti"`
}

type Verifier struct {
	Secret   []byte
	Audience string
	Now      func() time.Time
}

func (v Verifier) Verify(token string) (Claims, error) {
	if len(v.Secret) == 0 || len(token) == 0 || len(token) > 4096 {
		return Claims{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Claims{}, ErrInvalidToken
	}
	payload, err := decodeCanonical(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	signature, err := decodeCanonical(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return Claims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, v.Secret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Claims{}, ErrInvalidToken
	}

	var claims Claims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Claims{}, ErrInvalidToken
	}

	audience := strings.TrimSpace(v.Audience)
	if audience == "" {
		audience = "voice-sfu"
	}
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	checkedAt := now.Unix()
	if claims.Audience != audience || claims.ClientType != "client" ||
		!validText(claims.Room, 120) || !validText(claims.ParticipantID, 128) ||
		!validText(claims.TokenID, 128) || claims.IssuedAt > checkedAt+5 ||
		claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > 60 {
		return Claims{}, ErrInvalidToken
	}
	if claims.ExpiresAt <= checkedAt {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

func decodeCanonical(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, ErrInvalidToken
	}
	return decoded, nil
}

func validText(value string, max int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= max
}
