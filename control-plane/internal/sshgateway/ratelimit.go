// ratelimit.go provides brute-force protection for the gateway listener:
// a per-IP failed-authentication limiter with a temporary ban.

package sshgateway

import (
	"sync"
	"time"
)

const (
	maxFailures   = 10
	failureWindow = time.Minute
	banDuration   = 5 * time.Minute
)

type ipState struct {
	failures    []time.Time
	bannedUntil time.Time
}

type ipLimiter struct {
	mu     sync.Mutex
	states map[string]*ipState
	now    func() time.Time // overridable for tests
}

func newIPLimiter() *ipLimiter {
	return &ipLimiter{states: make(map[string]*ipState), now: time.Now}
}

// Allow reports whether the IP may attempt a connection.
func (l *ipLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.states[ip]
	if !ok {
		return true
	}
	return l.now().After(s.bannedUntil)
}

// RecordFailure notes a failed authentication; too many within the window
// bans the IP.
func (l *ipLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	s, ok := l.states[ip]
	if !ok {
		s = &ipState{}
		l.states[ip] = s
	}
	recent := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < failureWindow {
			recent = append(recent, t)
		}
	}
	s.failures = append(recent, now)
	if len(s.failures) >= maxFailures {
		s.bannedUntil = now.Add(banDuration)
		s.failures = nil
	}
}

// RecordSuccess clears the IP's failure history.
func (l *ipLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, ip)
}
