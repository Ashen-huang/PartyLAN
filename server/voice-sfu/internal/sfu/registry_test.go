package sfu

import (
	"errors"
	"sync"
	"testing"

	"github.com/pion/rtp"
)

type fakeMember struct {
	mu        sync.Mutex
	closed    bool
	closeCode string
}

func (m *fakeMember) Close(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.closeCode = code
}

func admitFake(t *testing.T, registry *Registry, room, id string) *fakeMember {
	t.Helper()
	member := &fakeMember{}
	got, err := registry.Admit(room, id, func() (Member, error) { return member, nil })
	if err != nil {
		t.Fatalf("Admit(%s, %s): %v", room, id, err)
	}
	if got != member {
		t.Fatalf("Admit returned %#v, want %#v", got, member)
	}
	return member
}

func TestRegistryIsolatesRoomsAndReplacesSameParticipant(t *testing.T) {
	registry := NewRegistry(20)
	old := admitFake(t, registry, "room-a", "client-1")
	admitFake(t, registry, "room-a", "client-2")
	admitFake(t, registry, "room-b", "client-3")

	if got := len(registry.Members("room-a")); got != 2 {
		t.Fatalf("room-a members = %d, want 2", got)
	}
	if _, exists := registry.Members("room-a")["client-3"]; exists {
		t.Fatal("cross-room member leaked into room-a")
	}

	replacement := admitFake(t, registry, "room-a", "client-1")
	if !old.closed || old.closeCode != "voice_replaced" {
		t.Fatalf("old connection close = (%v, %q)", old.closed, old.closeCode)
	}
	if registry.Members("room-a")["client-1"] != replacement {
		t.Fatal("replacement is not the current room member")
	}
	rooms, connections := registry.Counts()
	if rooms != 2 || connections != 3 {
		t.Fatalf("counts = (%d, %d), want (2, 3)", rooms, connections)
	}
}

func TestRegistryRejectsTwentyFirstUniqueMemberBeforeFactory(t *testing.T) {
	registry := NewRegistry(20)
	for index := 0; index < 20; index++ {
		admitFake(t, registry, "room", string(rune('a'+index)))
	}
	created := false
	_, err := registry.Admit("room", "twenty-first", func() (Member, error) {
		created = true
		return &fakeMember{}, nil
	})
	if !errors.Is(err, ErrRoomFull) {
		t.Fatalf("Admit error = %v, want room full", err)
	}
	if created {
		t.Fatal("member factory ran for rejected twenty-first participant")
	}
}

func TestRegistryLeaveDoesNotRemoveReplacementAndCleansEmptyRooms(t *testing.T) {
	registry := NewRegistry(20)
	old := admitFake(t, registry, "room", "client")
	replacement := admitFake(t, registry, "room", "client")
	registry.Leave("room", "client", old)
	if registry.Members("room")["client"] != replacement {
		t.Fatal("stale leave removed the replacement")
	}
	registry.Leave("room", "client", replacement)
	rooms, connections := registry.Counts()
	if rooms != 0 || connections != 0 {
		t.Fatalf("counts after final leave = (%d, %d)", rooms, connections)
	}
}

type packetWriter struct {
	packets []*rtp.Packet
}

func (w *packetWriter) WriteRTP(packet *rtp.Packet) error {
	clone := *packet
	clone.Payload = append([]byte(nil), packet.Payload...)
	w.packets = append(w.packets, &clone)
	return nil
}

func TestRelayForwardsOpusRTPToOthersButNeverSource(t *testing.T) {
	relay := NewRelay("source")
	source := &packetWriter{}
	peerA := &packetWriter{}
	peerB := &packetWriter{}
	relay.AddOutput("source", source)
	relay.AddOutput("peer-a", peerA)
	relay.AddOutput("peer-b", peerB)

	packet := &rtp.Packet{Header: rtp.Header{SequenceNumber: 9}, Payload: []byte{1, 2, 3}}
	if err := relay.Forward(packet); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if len(source.packets) != 0 || len(peerA.packets) != 1 || len(peerB.packets) != 1 {
		t.Fatalf("packet counts source=%d peer-a=%d peer-b=%d", len(source.packets), len(peerA.packets), len(peerB.packets))
	}
}

func TestNegotiationGateSerializesOffersAndRejectsStaleSignals(t *testing.T) {
	var gate NegotiationGate
	revision, start := gate.Request()
	if revision != 1 || !start {
		t.Fatalf("first request = (%d, %v), want (1, true)", revision, start)
	}
	if next, start := gate.Request(); next != 0 || start {
		t.Fatalf("request while awaiting = (%d, %v), want pending", next, start)
	}
	if gate.AcceptsCandidate(0) || !gate.AcceptsCandidate(1) {
		t.Fatal("candidate revision acceptance is incorrect")
	}
	if _, _, err := gate.Answer(0); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("old answer error = %v", err)
	}
	revision, start, err := gate.Answer(1)
	if err != nil || revision != 2 || !start {
		t.Fatalf("answer current = (%d, %v, %v), want next revision", revision, start, err)
	}
	if _, _, err := gate.Answer(1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("repeated old answer error = %v", err)
	}
}

func TestRegistryConcurrentReplacementAndLeaveDoesNotLeakMembers(t *testing.T) {
	registry := NewRegistry(20)
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			member := &fakeMember{}
			admitted, err := registry.Admit("room", "client", func() (Member, error) {
				return member, nil
			})
			if err == nil {
				registry.Leave("room", "client", admitted)
			}
		}()
	}
	group.Wait()
	rooms, connections := registry.Counts()
	if rooms != 0 || connections != 0 {
		t.Fatalf("concurrent cleanup counts = (%d, %d)", rooms, connections)
	}
}
