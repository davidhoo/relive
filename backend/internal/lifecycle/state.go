package lifecycle

import "sync/atomic"

// State holds lightweight process lifecycle flags shared across HTTP handlers
// and shutdown orchestration.
type State struct {
	draining atomic.Bool
}

func NewState() *State {
	return &State{}
}

func (s *State) BeginDraining() {
	if s == nil {
		return
	}
	s.draining.Store(true)
}

func (s *State) IsDraining() bool {
	if s == nil {
		return false
	}
	return s.draining.Load()
}
