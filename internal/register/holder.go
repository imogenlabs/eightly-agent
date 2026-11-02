// SPDX-License-Identifier: Apache-2.0

package register

import "sync/atomic"

// StateHolder is the agent's registration state, shared between the heartbeat,
// which swaps in a new State when it renews the token, and every request, which
// reads the agent ID to check a token's subject. A stored State is never
// changed afterwards; an update is always a new pointer, so readers need no lock.
type StateHolder struct{ p atomic.Pointer[State] }

// NewStateHolder returns a holder with s in it; s may be nil (not registered).
func NewStateHolder(s *State) *StateHolder {
	h := &StateHolder{}
	h.p.Store(s)
	return h
}

// Load returns the current state, or nil before the agent has registered.
func (h *StateHolder) Load() *State { return h.p.Load() }

// Store replaces the current state. The caller must not change s afterwards.
func (h *StateHolder) Store(s *State) { h.p.Store(s) }

// AgentID is this agent's ID, or "" before it has registered.
func (h *StateHolder) AgentID() string {
	if s := h.p.Load(); s != nil {
		return s.AgentID
	}
	return ""
}
