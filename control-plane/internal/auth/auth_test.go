package auth

import (
	"sync"
	"testing"
	"time"
)

func TestHashPassword_Roundtrip(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("mysecretpassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword("mysecretpassword", hash) {
		t.Error("CheckPassword returned false for correct password")
	}
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correctpassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if CheckPassword("wrongpassword", hash) {
		t.Error("CheckPassword returned true for wrong password")
	}
}

func TestCheckPassword_EmptyHash(t *testing.T) {
	t.Parallel()
	if CheckPassword("anything", "") {
		t.Error("CheckPassword returned true for empty hash")
	}
}

func TestCheckPassword_EmptyPassword(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("realpassword")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if CheckPassword("", hash) {
		t.Error("CheckPassword returned true for empty password")
	}
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	sessionID, err := store.Create(42)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sessionID == "" {
		t.Fatal("Create returned empty session ID")
	}

	userID, ok := store.Get(sessionID)
	if !ok {
		t.Fatal("Get returned false for valid session")
	}
	if userID != 42 {
		t.Errorf("Get returned userID %d, want 42", userID)
	}
}

func TestSessionStore_GetNonExistent(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("Get returned true for nonexistent session")
	}
}

func TestSessionStore_GetExpiredSession(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	sessionID, _ := store.Create(1)

	// Manually expire the session
	store.mu.Lock()
	entry := store.sessions[sessionID]
	entry.ExpiresAt = time.Now().Add(-1 * time.Second)
	store.sessions[sessionID] = entry
	store.mu.Unlock()

	_, ok := store.Get(sessionID)
	if ok {
		t.Error("Get returned true for expired session")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	sessionID, _ := store.Create(1)
	store.Delete(sessionID)

	_, ok := store.Get(sessionID)
	if ok {
		t.Error("Get returned true after Delete")
	}
}

func TestSessionStore_DeleteByUserID(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	s1, _ := store.Create(10)
	s2, _ := store.Create(10)
	s3, _ := store.Create(20) // different user

	store.DeleteByUserID(10)

	if _, ok := store.Get(s1); ok {
		t.Error("session s1 still exists after DeleteByUserID")
	}
	if _, ok := store.Get(s2); ok {
		t.Error("session s2 still exists after DeleteByUserID")
	}
	if _, ok := store.Get(s3); !ok {
		t.Error("session s3 for different user was deleted")
	}
}

func TestSessionStore_DeleteByUserIDExcept(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	s1, _ := store.Create(10)
	s2, _ := store.Create(10)
	s3, _ := store.Create(10)

	store.DeleteByUserIDExcept(10, s2)

	if _, ok := store.Get(s1); ok {
		t.Error("session s1 should have been deleted")
	}
	if _, ok := store.Get(s2); !ok {
		t.Error("session s2 (excepted) should still exist")
	}
	if _, ok := store.Get(s3); ok {
		t.Error("session s3 should have been deleted")
	}
}

func TestSessionStore_Cleanup(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	expired, _ := store.Create(1)
	valid, _ := store.Create(2)

	// Expire one session
	store.mu.Lock()
	entry := store.sessions[expired]
	entry.ExpiresAt = time.Now().Add(-1 * time.Second)
	store.sessions[expired] = entry
	store.mu.Unlock()

	store.Cleanup()

	if _, ok := store.Get(expired); ok {
		t.Error("expired session still exists after Cleanup")
	}
	if _, ok := store.Get(valid); !ok {
		t.Error("valid session was removed by Cleanup")
	}
}

func TestSessionStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(uid uint) {
			defer wg.Done()
			id, err := store.Create(uid)
			if err != nil {
				return
			}
			store.Get(id)
			store.Delete(id)
		}(uint(i))
	}
	wg.Wait()
}

func TestSessionCreate_UniqueIDs(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	seen := make(map[string]bool, 1000)

	for i := 0; i < 1000; i++ {
		id, err := store.Create(1)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate session ID on iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}
