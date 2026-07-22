package gamecast

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

type UntrustedCertificateError struct {
	Fingerprint string
	Err         error
}

func (e *UntrustedCertificateError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return "untrusted server certificate"
	}
	return "untrusted server certificate: " + e.Err.Error()
}

func (e *UntrustedCertificateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func TLSConfig(profile Profile) (*tls.Config, error) {
	normalized, err := NormalizeBaseURL(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "wss" {
		return nil, nil
	}

	serverName := parsed.Hostname()
	trustedFingerprint := NormalizeFingerprint(profile.TrustedCertificateSHA256)
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("server did not present a certificate")
			}
			leaf := state.PeerCertificates[0]
			fingerprint := FingerprintSHA256(leaf)
			if trustedFingerprint != "" && EqualFingerprint(fingerprint, trustedFingerprint) {
				return nil
			}

			intermediates := x509.NewCertPool()
			for _, cert := range state.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{
				DNSName:       serverName,
				Intermediates: intermediates,
			}); err != nil {
				return &UntrustedCertificateError{
					Fingerprint: fingerprint,
					Err:         err,
				}
			}
			return nil
		},
	}, nil
}

func FetchCertificateFingerprint(ctx context.Context, baseURL string) (string, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" {
		return "", nil
	}

	host := parsed.Host
	if parsed.Port() == "" {
		host = net.JoinHostPort(parsed.Hostname(), "443")
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config: &tls.Config{
			ServerName:         parsed.Hostname(),
			InsecureSkipVerify: true,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return "", fmt.Errorf("dial server tls: %w", err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("unexpected tls connection type %T", conn)
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("server did not present a certificate")
	}
	return FingerprintSHA256(state.PeerCertificates[0]), nil
}
