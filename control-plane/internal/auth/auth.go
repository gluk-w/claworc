package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionDuration = 1 * time.Hour
	SessionCookie   = "claworc_session"
	BcryptCost      = 12
)

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type sessionEntry struct {
	UserID    uint
	ExpiresAt time.Time
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]sessionEntry
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]sessionEntry),
	}
}

func (s *SessionStore) Create(userID uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[id] = sessionEntry{
		UserID:    userID,
		ExpiresAt: time.Now().Add(SessionDuration),
	}
	s.mu.Unlock()
	return id, nil
}

func (s *SessionStore) Get(sessionID string) (uint, bool) {
	s.mu.RLock()
	entry, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok || time.Now().After(entry.ExpiresAt) {
		return 0, false
	}
	return entry.UserID, true
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

func (s *SessionStore) DeleteByUserID(userID uint) {
	s.mu.Lock()
	for id, entry := range s.sessions {
		if entry.UserID == userID {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

func (s *SessionStore) Cleanup() {
	now := time.Now()
	s.mu.Lock()
	for id, entry := range s.sessions {
		if now.After(entry.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}
