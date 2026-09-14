package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func signedToken(t *testing.T, secret string, claims Claims) string {
	t.Helper()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validClaims() Claims {
	return Claims{
		Audience:      "voice-sfu",
		Room:          "room-1",
		ParticipantID: "client-1",
		ClientType:    "client",
		IssuedAt:      1_000,
		ExpiresAt:     1_015,
		TokenID:       "jti-1",
	}
}

func verifier() Verifier {
	return Verifier{
		Secret:   []byte("test-secret"),
		Audience: "voice-sfu",
		Now:      func() time.Time { return time.Unix(1_005, 0) },
	}
}

func TestVerifierAcceptsPresenceBoundClientToken(t *testing.T) {
	want := validClaims()
	got, err := verifier().Verify(signedToken(t, "test-secret", want))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != want {
		t.Fatalf("claims = %#v, want %#v", got, want)
	}
}

func TestVerifierRejectsTamperingExpiryAudienceAndWebClients(t *testing.T) {
	tests := []struct {
		name   string
		token  func(*testing.T) string
		wanted error
	}{
		{
			name: "tampered signature",
			token: func(t *testing.T) string {
				token := signedToken(t, "test-secret", validClaims())
				parts := strings.Split(token, ".")
				if parts[1][0] == 'A' {
					parts[1] = "B" + parts[1][1:]
				} else {
					parts[1] = "A" + parts[1][1:]
				}
				return strings.Join(parts, ".")
			},
			wanted: ErrInvalidToken,
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.ExpiresAt = 1_005
				return signedToken(t, "test-secret", claims)
			},
			wanted: ErrExpiredToken,
		},
		{
			name: "wrong audience",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.Audience = "screen-sfu"
				return signedToken(t, "test-secret", claims)
			},
			wanted: ErrInvalidToken,
		},
		{
			name: "web client",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.ClientType = "web"
				return signedToken(t, "test-secret", claims)
			},
			wanted: ErrInvalidToken,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifier().Verify(test.token(t))
			if !errors.Is(err, test.wanted) {
				t.Fatalf("Verify error = %v, want %v", err, test.wanted)
			}
		})
	}
}

func TestVerifierRejectsInvalidFieldBoundaries(t *testing.T) {
	tests := []Claims{
		func() Claims { value := validClaims(); value.Room = strings.Repeat("r", 121); return value }(),
		func() Claims { value := validClaims(); value.ParticipantID = strings.Repeat("p", 129); return value }(),
		func() Claims { value := validClaims(); value.TokenID = ""; return value }(),
		func() Claims { value := validClaims(); value.IssuedAt = 1_011; return value }(),
		func() Claims { value := validClaims(); value.ExpiresAt = value.IssuedAt + 61; return value }(),
	}
	for index, claims := range tests {
		if _, err := verifier().Verify(signedToken(t, "test-secret", claims)); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("case %d error = %v, want invalid", index, err)
		}
	}
}
