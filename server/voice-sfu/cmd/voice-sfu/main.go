package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	voicesfu "PartyLAN/server/voice-sfu/internal/sfu"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatalf("[voice-sfu] startup failed: %v", err)
	}
}

func run() error {
	secret := strings.TrimSpace(os.Getenv("GAMECAST_VOICE_SECRET"))
	if secret == "" {
		return errors.New("GAMECAST_VOICE_SECRET is required")
	}
	listenAddress := envOrDefault("VOICE_SFU_LISTEN_ADDR", "127.0.0.1:5054")
	udpAddress := envOrDefault("VOICE_SFU_UDP_ADDR", "0.0.0.0:5054")
	maxParticipants, err := envInt("VOICE_SFU_ROOM_LIMIT", 20)
	if err != nil || maxParticipants != 20 {
		return errors.New("VOICE_SFU_ROOM_LIMIT must be 20")
	}
	iceServers, err := parseICEServers(os.Getenv("VOICE_SFU_ICE_SERVERS_JSON"))
	if err != nil {
		return err
	}

	udpAddr, err := net.ResolveUDPAddr("udp4", udpAddress)
	if err != nil {
		return fmt.Errorf("resolve ICE UDP address: %w", err)
	}
	udpConn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return fmt.Errorf("listen ICE UDP: %w", err)
	}
	udpMux := ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: udpConn})
	defer func() { _ = udpMux.Close() }()

	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetICEUDPMux(udpMux)
	if publicIP := strings.TrimSpace(os.Getenv("VOICE_SFU_PUBLIC_IP")); publicIP != "" {
		if net.ParseIP(publicIP) == nil {
			return errors.New("VOICE_SFU_PUBLIC_IP is invalid")
		}
		settingEngine.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
	voiceServer, err := voicesfu.NewServer(voicesfu.Config{
		Version:         version,
		Secret:          []byte(secret),
		MaxParticipants: maxParticipants,
		API:             api,
		ICEServers:      iceServers,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              listenAddress,
		Handler:           voiceServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErrors := make(chan error, 1)
	go func() {
		log.Printf("[voice-sfu] version=%s tcp=%s udp=%s", version, listenAddress, udpAddress)
		serveErrors <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	}
}

func parseICEServers(value string) ([]webrtc.ICEServer, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var servers []webrtc.ICEServer
	if err := json.Unmarshal([]byte(value), &servers); err != nil {
		return nil, errors.New("VOICE_SFU_ICE_SERVERS_JSON is invalid")
	}
	for _, server := range servers {
		if len(server.URLs) == 0 {
			return nil, errors.New("VOICE_SFU_ICE_SERVERS_JSON contains an empty server")
		}
	}
	return servers, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}
