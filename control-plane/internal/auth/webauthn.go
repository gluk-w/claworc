package auth

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/glukw/claworc/internal/database"
)

var WebAuthn *webauthn.WebAuthn

func InitWebAuthn(rpID string, rpOrigins []string) error {
	var err error
	WebAuthn, err = webauthn.New(&webauthn.Config{
		RPDisplayName: "Claworc",
		RPID:          rpID,
		RPOrigins:     rpOrigins,
	})
	return err
}

// challengeStore holds in-flight WebAuthn challenges with a 2-minute TTL.
var challengeStore = struct {
	sync.Mutex
	entries map[uint]challengeEntry
}{entries: make(map[uint]challengeEntry)}

type challengeEntry struct {
	SessionData *webauthn.SessionData
	ExpiresAt   time.Time
}

func StoreChallenge(userID uint, sd *webauthn.SessionData) {
	challengeStore.Lock()
	challengeStore.entries[userID] = challengeEntry{
		SessionData: sd,
		ExpiresAt:   time.Now().Add(2 * time.Minute),
	}
	challengeStore.Unlock()
}

func GetChallenge(userID uint) (*webauthn.SessionData, bool) {
	challengeStore.Lock()
	defer challengeStore.Unlock()
	entry, ok := challengeStore.entries[userID]
	if !ok || time.Now().After(entry.ExpiresAt) {
		delete(challengeStore.entries, userID)
		return nil, false
	}
	delete(challengeStore.entries, userID)
	return entry.SessionData, true
}

// WebAuthnUser adapts database.User to webauthn.User interface.
type WebAuthnUser struct {
	User        database.User
	Credentials []webauthn.Credential
}

func LoadWebAuthnUser(userID uint) (*WebAuthnUser, error) {
	user, err := database.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	return loadWebAuthnUserFromDB(user)
}

func LoadWebAuthnUserByName(username string) (*WebAuthnUser, error) {
	user, err := database.GetUserByUsername(username)
	if err != nil {
		return nil, err
	}
	return loadWebAuthnUserFromDB(user)
}

func loadWebAuthnUserFromDB(user *database.User) (*WebAuthnUser, error) {
	dbCreds, err := database.GetWebAuthnCredentials(user.ID)
	if err != nil {
		return nil, err
	}

	creds := make([]webauthn.Credential, 0, len(dbCreds))
	for _, dc := range dbCreds {
		var transports []protocol.AuthenticatorTransport
		if dc.Transport != "" {
			for _, t := range strings.Split(dc.Transport, ",") {
				transports = append(transports, protocol.AuthenticatorTransport(t))
			}
		}
		creds = append(creds, webauthn.Credential{
			ID:              []byte(dc.ID),
			PublicKey:       dc.PublicKey,
			AttestationType: dc.AttestationType,
			Transport:       transports,
			Authenticator: webauthn.Authenticator{
				SignCount: dc.SignCount,
				AAGUID:    dc.AAGUID,
			},
		})
	}

	return &WebAuthnUser{
		User:        *user,
		Credentials: creds,
	}, nil
}

func (u *WebAuthnUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(u.User.ID))
	return b
}

func (u *WebAuthnUser) WebAuthnName() string {
	return u.User.Username
}

func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.User.Username
}

func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.Credentials
}

// SaveCredential persists a new webauthn.Credential for the user.
func SaveCredential(userID uint, name string, cred *webauthn.Credential) error {
	var transports []string
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}

	dbCred := &database.WebAuthnCredential{
		ID:              string(cred.ID),
		UserID:          userID,
		Name:            name,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		Transport:       strings.Join(transports, ","),
		SignCount:       cred.Authenticator.SignCount,
		AAGUID:          cred.Authenticator.AAGUID,
	}
	return database.SaveWebAuthnCredential(dbCred)
}

// DiscoverableLogin looks up a user by the credential ID returned during discoverable login.
func DiscoverableLogin(rawID, userHandle []byte) (webauthn.User, error) {
	if len(userHandle) < 8 {
		return nil, fmt.Errorf("invalid user handle")
	}
	userID := uint(binary.BigEndian.Uint64(userHandle))
	wau, err := LoadWebAuthnUser(userID)
	if err != nil {
		return nil, err
	}
	return wau, nil
}
