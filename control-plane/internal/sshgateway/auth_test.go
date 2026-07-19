package sshgateway

import (
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/database/migrations"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	// A pooled second connection to ":memory:" would see a fresh empty
	// database; force a single connection so concurrent queries share it.
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := migrations.AutoMigrateAll(db); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

// generateUserKey creates a key pair, registers the public key for the user,
// and returns the signer for authenticating.
func generateUserKey(t *testing.T, userID uint) ssh.Signer {
	t.Helper()
	pubKey, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if err := database.CreateUserSSHKey(&database.UserSSHKey{
		UserID:      userID,
		PublicKey:   string(pubKey),
		Fingerprint: ssh.FingerprintSHA256(parsed),
	}); err != nil {
		t.Fatalf("create user ssh key: %v", err)
	}
	return signer
}

type fakeConnMetadata struct {
	user string
}

func (m fakeConnMetadata) User() string          { return m.user }
func (m fakeConnMetadata) SessionID() []byte     { return nil }
func (m fakeConnMetadata) ClientVersion() []byte { return nil }
func (m fakeConnMetadata) ServerVersion() []byte { return nil }
func (m fakeConnMetadata) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}
func (m fakeConnMetadata) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2222}
}

func seedUser(t *testing.T, username, role string) *database.User {
	t.Helper()
	u := &database.User{Username: username, PasswordHash: "x", Role: role}
	if err := database.CreateUser(u); err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return u
}

func seedInstance(t *testing.T, name string, teamID uint) *database.Instance {
	t.Helper()
	inst := &database.Instance{Name: name, DisplayName: name, TeamID: teamID}
	if err := database.DB.Create(inst).Error; err != nil {
		t.Fatalf("create instance %s: %v", name, err)
	}
	return inst
}

func authPubKey(t *testing.T, signer ssh.Signer) ssh.PublicKey {
	t.Helper()
	return signer.PublicKey()
}

func TestAuthenticate(t *testing.T) {
	setupTestDB(t)
	g := New(Config{})

	team := &database.Team{Name: "team-a"}
	if err := database.DB.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	admin := seedUser(t, "admin", "admin")
	manager := seedUser(t, "manager", "user")
	member := seedUser(t, "member", "user")
	stranger := seedUser(t, "stranger", "user")

	database.DB.Create(&database.TeamMember{TeamID: team.ID, UserID: manager.ID, Role: database.TeamRoleManager})
	database.DB.Create(&database.TeamMember{TeamID: team.ID, UserID: member.ID, Role: database.TeamRoleUser})
	database.DB.Create(&database.TeamMember{TeamID: team.ID, UserID: stranger.ID, Role: database.TeamRoleUser})

	inst := seedInstance(t, "bot-my-agent", team.ID)
	database.DB.Create(&database.UserInstance{UserID: member.ID, InstanceID: inst.ID})

	adminKey := generateUserKey(t, admin.ID)
	managerKey := generateUserKey(t, manager.ID)
	memberKey := generateUserKey(t, member.ID)
	strangerKey := generateUserKey(t, stranger.ID)

	tests := []struct {
		name       string
		sshUser    string
		signer     ssh.Signer
		wantErr    bool
		wantDeny   string
		wantInstID bool
	}{
		{"admin full access", "admin+my-agent", adminKey, false, "", true},
		{"admin with bot prefix", "admin+bot-my-agent", adminKey, false, "", true},
		{"team manager", "manager+my-agent", managerKey, false, "", true},
		{"granted member", "member+my-agent", memberKey, false, "", true},
		{"ungranted team user", "stranger+my-agent", strangerKey, false, denyUnknownInstance, false},
		{"unknown instance", "admin+nope", adminKey, false, denyUnknownInstance, false},
		{"missing instance", "admin", adminKey, false, denyMissingInstance, false},
		{"unknown user", "ghost+my-agent", adminKey, true, "", false},
		{"wrong user's key", "admin+my-agent", strangerKey, true, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perms, err := g.authenticate(fakeConnMetadata{user: tt.sshUser}, authPubKey(t, tt.signer))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected authentication error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("authenticate: %v", err)
			}
			if got := perms.Extensions[extDenyReason]; got != tt.wantDeny {
				t.Errorf("deny reason = %q, want %q", got, tt.wantDeny)
			}
			if hasInst := perms.Extensions[extInstanceID] != ""; hasInst != tt.wantInstID {
				t.Errorf("instance ID set = %v, want %v", hasInst, tt.wantInstID)
			}
		})
	}
}

func TestAuthenticateUnregisteredKey(t *testing.T) {
	setupTestDB(t)
	g := New(Config{})
	seedUser(t, "alice", "admin")

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, _ := ssh.ParsePrivateKey(privKeyPEM)

	if _, err := g.authenticate(fakeConnMetadata{user: "alice+x"}, signer.PublicKey()); err == nil {
		t.Fatal("expected error for unregistered key")
	}
}
