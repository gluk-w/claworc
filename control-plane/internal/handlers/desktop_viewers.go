package handlers

import (
	"sync"
	"sync/atomic"
)

// viewerSession represents a single connected desktop client. The websocket
// proxy creates one on each accept and tears it down on close. The registry
// elects exactly one primary per instance (the head of the per-instance list)
// so that only that client's RFB SetDesktopSize messages are forwarded to the
// X server.
type viewerSession struct {
	primary atomic.Bool

	mu                 sync.Mutex
	lastSetDesktopSize []byte // raw RFB ClientSetDesktopSize bytes; replayed on promotion

	// inject pushes a synthetic byte slice into the upstream noVNC connection
	// for this session. Set by the websocket proxy. Used to replay the last
	// SetDesktopSize when a secondary is promoted to primary.
	inject func([]byte)
}

func (s *viewerSession) isPrimary() bool { return s.primary.Load() }

func (s *viewerSession) recordSetDesktopSize(msg []byte) {
	s.mu.Lock()
	s.lastSetDesktopSize = append(s.lastSetDesktopSize[:0], msg...)
	s.mu.Unlock()
}

func (s *viewerSession) replaySetDesktopSize() {
	s.mu.Lock()
	msg := append([]byte(nil), s.lastSetDesktopSize...)
	inject := s.inject
	s.mu.Unlock()
	if len(msg) == 0 || inject == nil {
		return
	}
	inject(msg)
}

type viewerRegistry struct {
	mu      sync.Mutex
	perInst map[uint][]*viewerSession // ordered: head is primary
}

var viewers = &viewerRegistry{perInst: make(map[uint][]*viewerSession)}

// Join registers a new viewer. The first joiner is primary; everyone else is
// secondary until promoted.
func (r *viewerRegistry) Join(instanceID uint) *viewerSession {
	s := &viewerSession{}
	r.mu.Lock()
	r.perInst[instanceID] = append(r.perInst[instanceID], s)
	if r.perInst[instanceID][0] == s {
		s.primary.Store(true)
	}
	r.mu.Unlock()
	return s
}

// Leave removes a viewer. If it was primary, the next-oldest viewer is
// promoted and its last-attempted SetDesktopSize is replayed upstream so the
// X display snaps back to the new primary's panel size.
func (r *viewerRegistry) Leave(instanceID uint, s *viewerSession) {
	r.mu.Lock()
	list := r.perInst[instanceID]
	idx := -1
	for i, v := range list {
		if v == s {
			idx = i
			break
		}
	}
	var promoted *viewerSession
	if idx >= 0 {
		wasPrimary := idx == 0
		list = append(list[:idx], list[idx+1:]...)
		if len(list) == 0 {
			delete(r.perInst, instanceID)
		} else {
			r.perInst[instanceID] = list
			if wasPrimary {
				promoted = list[0]
				promoted.primary.Store(true)
			}
		}
	}
	r.mu.Unlock()
	if promoted != nil {
		promoted.replaySetDesktopSize()
	}
}
