package sfu

import (
	"errors"
	"sync"

	"github.com/pion/rtp"
)

var (
	ErrRoomFull      = errors.New("voice room full")
	ErrInvalidMember = errors.New("voice member invalid")
	ErrStaleRevision = errors.New("stale negotiation revision")
)

type Member interface {
	Close(code string)
}

type Registry struct {
	mu    sync.RWMutex
	max   int
	rooms map[string]map[string]Member
}

func NewRegistry(max int) *Registry {
	if max <= 0 {
		max = 20
	}
	return &Registry{max: max, rooms: make(map[string]map[string]Member)}
}

func (r *Registry) Admit(room, participantID string, factory func() (Member, error)) (Member, error) {
	if room == "" || participantID == "" || factory == nil {
		return nil, ErrInvalidMember
	}
	r.mu.Lock()
	members := r.rooms[room]
	if members == nil {
		members = make(map[string]Member)
	}
	old, replacing := members[participantID]
	if !replacing && len(members) >= r.max {
		r.mu.Unlock()
		return nil, ErrRoomFull
	}
	member, err := factory()
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if member == nil {
		r.mu.Unlock()
		return nil, ErrInvalidMember
	}
	members[participantID] = member
	r.rooms[room] = members
	r.mu.Unlock()
	if old != nil && old != member {
		old.Close("voice_replaced")
	}
	return member, nil
}

func (r *Registry) Leave(room, participantID string, member Member) {
	if member == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	members := r.rooms[room]
	if members == nil || members[participantID] != member {
		return
	}
	delete(members, participantID)
	if len(members) == 0 {
		delete(r.rooms, room)
	}
}

func (r *Registry) Members(room string) map[string]Member {
	r.mu.RLock()
	defer r.mu.RUnlock()
	members := r.rooms[room]
	snapshot := make(map[string]Member, len(members))
	for participantID, member := range members {
		snapshot[participantID] = member
	}
	return snapshot
}

func (r *Registry) Counts() (int, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connections := 0
	for _, members := range r.rooms {
		connections += len(members)
	}
	return len(r.rooms), connections
}

type RTPWriter interface {
	WriteRTP(packet *rtp.Packet) error
}

type Relay struct {
	mu      sync.RWMutex
	source  string
	outputs map[string]RTPWriter
}

func NewRelay(source string) *Relay {
	return &Relay{source: source, outputs: make(map[string]RTPWriter)}
}

func (r *Relay) AddOutput(participantID string, output RTPWriter) {
	if participantID == "" || participantID == r.source || output == nil {
		return
	}
	r.mu.Lock()
	r.outputs[participantID] = output
	r.mu.Unlock()
}

func (r *Relay) RemoveOutput(participantID string) {
	r.mu.Lock()
	delete(r.outputs, participantID)
	r.mu.Unlock()
}

func (r *Relay) Forward(packet *rtp.Packet) error {
	if packet == nil {
		return nil
	}
	r.mu.RLock()
	outputs := make([]RTPWriter, 0, len(r.outputs))
	for _, output := range r.outputs {
		outputs = append(outputs, output)
	}
	r.mu.RUnlock()
	var forwardedErrors []error
	for _, output := range outputs {
		if err := output.WriteRTP(packet.Clone()); err != nil {
			forwardedErrors = append(forwardedErrors, err)
		}
	}
	return errors.Join(forwardedErrors...)
}

type NegotiationGate struct {
	mu       sync.Mutex
	revision uint64
	awaiting uint64
	pending  bool
}

func (g *NegotiationGate) Request() (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.awaiting != 0 {
		g.pending = true
		return 0, false
	}
	g.revision++
	g.awaiting = g.revision
	return g.revision, true
}

func (g *NegotiationGate) Answer(revision uint64) (uint64, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.awaiting == 0 || revision != g.awaiting {
		return 0, false, ErrStaleRevision
	}
	g.awaiting = 0
	if !g.pending {
		return 0, false, nil
	}
	g.pending = false
	g.revision++
	g.awaiting = g.revision
	return g.revision, true, nil
}

func (g *NegotiationGate) AcceptsCandidate(revision uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return revision != 0 && revision == g.revision
}

func (g *NegotiationGate) AcceptsAnswer(revision uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return revision != 0 && revision == g.awaiting
}

func (g *NegotiationGate) CurrentRevision() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.revision
}
