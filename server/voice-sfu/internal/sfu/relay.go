package sfu

import (
	"sync"

	"github.com/pion/webrtc/v4"
)

type relayOutput struct {
	session *peerSession
	track   *webrtc.TrackLocalStaticRTP
	sender  *webrtc.RTPSender
}

type sourceRelay struct {
	source  *peerSession
	relay   *Relay
	mu      sync.Mutex
	closed  bool
	outputs map[*peerSession]*relayOutput
}

func (s *Server) publish(source *peerSession, remote *webrtc.TrackRemote) {
	relay := &sourceRelay{
		source:  source,
		relay:   NewRelay(source.relayKey),
		outputs: make(map[*peerSession]*relayOutput),
	}
	s.relayMu.Lock()
	roomRelays := s.relays[source.room]
	if roomRelays == nil {
		roomRelays = make(map[string]*sourceRelay)
		s.relays[source.room] = roomRelays
	}
	previous := roomRelays[source.participantID]
	roomRelays[source.participantID] = relay
	s.relayMu.Unlock()
	if previous != nil {
		s.closeRelay(previous)
	}

	for _, member := range s.registry.Members(source.room) {
		destination, ok := member.(*peerSession)
		if ok && destination != source {
			s.attachRelay(relay, destination)
		}
	}
	go s.forwardRemoteTrack(relay, remote)
}

func (s *Server) forwardRemoteTrack(relay *sourceRelay, remote *webrtc.TrackRemote) {
	for {
		packet, _, err := remote.ReadRTP()
		if err != nil {
			s.removeRelay(relay)
			return
		}
		_ = relay.relay.Forward(packet)
	}
}

func (s *Server) attachExistingRelays(destination *peerSession) {
	s.relayMu.Lock()
	roomRelays := s.relays[destination.room]
	relays := make([]*sourceRelay, 0, len(roomRelays))
	for _, relay := range roomRelays {
		if relay.source != destination {
			relays = append(relays, relay)
		}
	}
	s.relayMu.Unlock()
	for _, relay := range relays {
		s.attachRelay(relay, destination)
	}
}

func (s *Server) attachRelay(relay *sourceRelay, destination *peerSession) {
	if relay.source == destination {
		return
	}
	relay.mu.Lock()
	if relay.closed || relay.outputs[destination] != nil {
		relay.mu.Unlock()
		return
	}
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  2,
	}, trackID(relay.source.participantID), "voice-"+privateID(relay.source.room))
	if err != nil {
		relay.mu.Unlock()
		return
	}
	sender, err := destination.pc.AddTrack(track)
	if err != nil {
		relay.mu.Unlock()
		return
	}
	output := &relayOutput{session: destination, track: track, sender: sender}
	relay.outputs[destination] = output
	relay.relay.AddOutput(destination.relayKey, track)
	relay.mu.Unlock()
	destination.readRTCP(sender)
	if err := destination.requestOffer(); err != nil {
		destination.Close("voice_negotiation_failed")
	}
}

func (s *Server) removeRelay(relay *sourceRelay) {
	s.relayMu.Lock()
	roomRelays := s.relays[relay.source.room]
	if roomRelays != nil && roomRelays[relay.source.participantID] == relay {
		delete(roomRelays, relay.source.participantID)
		if len(roomRelays) == 0 {
			delete(s.relays, relay.source.room)
		}
	}
	s.relayMu.Unlock()
	s.closeRelay(relay)
}

func (s *Server) closeRelay(relay *sourceRelay) {
	relay.mu.Lock()
	if relay.closed {
		relay.mu.Unlock()
		return
	}
	relay.closed = true
	outputs := make([]*relayOutput, 0, len(relay.outputs))
	for _, output := range relay.outputs {
		outputs = append(outputs, output)
		relay.relay.RemoveOutput(output.session.relayKey)
	}
	relay.outputs = make(map[*peerSession]*relayOutput)
	relay.mu.Unlock()
	for _, output := range outputs {
		if err := output.session.pc.RemoveTrack(output.sender); err == nil {
			if err := output.session.requestOffer(); err != nil {
				output.session.Close("voice_negotiation_failed")
			}
		}
	}
}

func (s *Server) detach(session *peerSession) {
	s.relayMu.Lock()
	roomRelays := s.relays[session.room]
	relays := make([]*sourceRelay, 0, len(roomRelays))
	var own *sourceRelay
	for participantID, relay := range roomRelays {
		if participantID == session.participantID && relay.source == session {
			own = relay
			delete(roomRelays, participantID)
			continue
		}
		relays = append(relays, relay)
	}
	if len(roomRelays) == 0 {
		delete(s.relays, session.room)
	}
	s.relayMu.Unlock()
	if own != nil {
		s.closeRelay(own)
	}
	for _, relay := range relays {
		relay.mu.Lock()
		if output := relay.outputs[session]; output != nil {
			delete(relay.outputs, session)
			relay.relay.RemoveOutput(session.relayKey)
		}
		relay.mu.Unlock()
	}
}

var _ RTPWriter = (*webrtc.TrackLocalStaticRTP)(nil)
