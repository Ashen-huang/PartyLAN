package audio

import "sync"

type ShareSession struct {
	mu       sync.Mutex
	selected map[string]bool
	running  bool
}

func NewShareSession() *ShareSession {
	return &ShareSession{
		selected: make(map[string]bool),
	}
}

func (s *ShareSession) SetSelected(names []string) {
	next := make(map[string]bool, 1)
	for _, name := range names {
		if name != "" {
			next[name] = true
			break
		}
	}

	s.mu.Lock()
	s.selected = next
	s.mu.Unlock()
}

func (s *ShareSession) Selected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.selected))
	for name := range s.selected {
		out = append(out, name)
	}
	return out
}

func (s *ShareSession) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = true
	return nil
}

func (s *ShareSession) Stop() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *ShareSession) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
