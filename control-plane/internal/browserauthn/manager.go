// Package browserauthn installs CDP virtual WebAuthn authenticators (a
// simulated fingerprint reader and/or USB security key) into every tab of an
// instance's on-demand browser, and mirrors every credential they create into
// the control-plane database so passkeys survive browser pod restarts.
//
// Chromium's virtual authenticators live only as long as the CDP session that
// created them and are per-tab, so the Manager keeps one long-lived
// browser-level CDP connection per running browser session:
//
//   - Target.setAutoAttach(waitForDebuggerOnStart) pauses every new page until
//     the authenticator(s) and all stored credentials are installed.
//   - WebAuthn.credentialAdded/Asserted/Updated/Deleted events are written
//     through to the browser_passkeys table and fanned out to the other tabs.
//   - A periodic reconcile (WebAuthn.getCredentials) catches missed events.
//
// See docs/virtual-authenticator.md.
package browserauthn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// Kind is a simulated authenticator type.
type Kind string

const (
	KindPlatform    Kind = "platform"     // built-in fingerprint reader (transport "internal")
	KindSecurityKey Kind = "security_key" // roaming USB security key (transport "usb")
)

// Setting values for Instance.VirtualAuthenticator.
const (
	SettingOff         = ""
	SettingPlatform    = "platform"
	SettingSecurityKey = "security_key"
	SettingBoth        = "both"
)

// ValidSetting reports whether s is an accepted Instance.VirtualAuthenticator value.
func ValidSetting(s string) bool {
	switch s {
	case SettingOff, SettingPlatform, SettingSecurityKey, SettingBoth:
		return true
	}
	return false
}

// KindsFor maps an Instance.VirtualAuthenticator value to authenticator kinds.
func KindsFor(setting string) []Kind {
	switch setting {
	case SettingPlatform:
		return []Kind{KindPlatform}
	case SettingSecurityKey:
		return []Kind{KindSecurityKey}
	case SettingBoth:
		return []Kind{KindPlatform, KindSecurityKey}
	}
	return nil
}

func authenticatorOptions(k Kind) map[string]interface{} {
	transport := "internal"
	if k == KindSecurityKey {
		transport = "usb"
	}
	return map[string]interface{}{
		"protocol":                    "ctap2",
		"ctap2Version":                "ctap2_1",
		"transport":                   transport,
		"hasResidentKey":              true,
		"hasUserVerification":         true,
		"isUserVerified":              true,
		"automaticPresenceSimulation": true,
		"hasLargeBlob":                true,
		"hasCredBlob":                 true,
	}
}

// credential mirrors CDP WebAuthn.Credential. Binary fields are base64.
type credential struct {
	CredentialID         string `json:"credentialId"`
	IsResidentCredential bool   `json:"isResidentCredential"`
	RpID                 string `json:"rpId,omitempty"`
	PrivateKey           string `json:"privateKey"`
	UserHandle           string `json:"userHandle,omitempty"`
	SignCount            uint32 `json:"signCount"`
	LargeBlob            string `json:"largeBlob,omitempty"`
	BackupEligibility    *bool  `json:"backupEligibility,omitempty"`
	BackupState          *bool  `json:"backupState,omitempty"`
	UserName             string `json:"userName,omitempty"`
	UserDisplayName      string `json:"userDisplayName,omitempty"`
}

// toRow encrypts a CDP credential into a DB row.
func toRow(instanceID uint, k Kind, c credential) (*database.BrowserPasskey, error) {
	pk, err := utils.Encrypt(c.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt private key: %w", err)
	}
	blob := ""
	if c.LargeBlob != "" {
		if blob, err = utils.Encrypt(c.LargeBlob); err != nil {
			return nil, fmt.Errorf("encrypt large blob: %w", err)
		}
	}
	row := &database.BrowserPasskey{
		InstanceID:      instanceID,
		Authenticator:   string(k),
		CredentialID:    c.CredentialID,
		RpID:            c.RpID,
		UserHandle:      c.UserHandle,
		UserName:        c.UserName,
		UserDisplayName: c.UserDisplayName,
		IsResident:      c.IsResidentCredential,
		PrivateKey:      pk,
		SignCount:       c.SignCount,
		LargeBlob:       blob,
	}
	if c.BackupEligibility != nil {
		row.BackupEligibility = *c.BackupEligibility
	}
	if c.BackupState != nil {
		row.BackupState = *c.BackupState
	}
	return row, nil
}

// fromRow decrypts a DB row into a CDP credential.
func fromRow(r database.BrowserPasskey) (credential, error) {
	pk, err := utils.Decrypt(r.PrivateKey)
	if err != nil {
		return credential{}, fmt.Errorf("decrypt private key: %w", err)
	}
	blob, err := utils.Decrypt(r.LargeBlob)
	if err != nil {
		return credential{}, fmt.Errorf("decrypt large blob: %w", err)
	}
	be, bs := r.BackupEligibility, r.BackupState
	return credential{
		CredentialID:         r.CredentialID,
		IsResidentCredential: r.IsResident,
		RpID:                 r.RpID,
		PrivateKey:           pk,
		UserHandle:           r.UserHandle,
		SignCount:            r.SignCount,
		LargeBlob:            blob,
		BackupEligibility:    &be,
		BackupState:          &bs,
		UserName:             r.UserName,
		UserDisplayName:      r.UserDisplayName,
	}, nil
}

// Store is the persistence the manager needs; the default implementation is
// backed by the database package. Swappable for tests.
type Store interface {
	InstanceSetting(instanceID uint) (string, error)
	RunningInstancesWithAuthenticator() ([]uint, error)
	List(instanceID uint) ([]database.BrowserPasskey, error)
	Upsert(row *database.BrowserPasskey) error
	RecordUse(instanceID uint, credentialID string, signCount uint32) error
	DeleteByCredential(instanceID uint, credentialID string) error
}

type dbStore struct{}

func (dbStore) InstanceSetting(instanceID uint) (string, error) {
	var inst database.Instance
	if err := database.DB.Select("id", "virtual_authenticator", "container_image").First(&inst, instanceID).Error; err != nil {
		return "", err
	}
	if database.IsLegacyEmbedded(inst.ContainerImage) {
		return SettingOff, nil
	}
	return inst.VirtualAuthenticator, nil
}

func (dbStore) RunningInstancesWithAuthenticator() ([]uint, error) {
	var ids []uint
	err := database.DB.Model(&database.Instance{}).
		Joins("JOIN browser_sessions ON browser_sessions.instance_id = instances.id").
		Where("instances.virtual_authenticator <> '' AND browser_sessions.status = ?", "running").
		Pluck("instances.id", &ids).Error
	return ids, err
}

func (dbStore) List(id uint) ([]database.BrowserPasskey, error) {
	return database.ListBrowserPasskeys(id)
}
func (dbStore) Upsert(r *database.BrowserPasskey) error { return database.UpsertBrowserPasskey(r) }
func (dbStore) RecordUse(id uint, cred string, n uint32) error {
	return database.RecordBrowserPasskeyUse(id, cred, n)
}
func (dbStore) DeleteByCredential(id uint, cred string) error {
	return database.DeleteBrowserPasskeyByCredential(id, cred)
}

// Manager owns one authenticator session per running browser.
type Manager struct {
	dial      DialFunc
	isRunning func(ctx context.Context, instanceID uint) bool
	store     Store

	// ReconcileInterval and SuperviseInterval are exported for tests.
	ReconcileInterval time.Duration
	SuperviseInterval time.Duration

	mu       sync.Mutex
	sessions map[uint]*instanceSession
	baseCtx  context.Context
}

// New builds a Manager. dial must not spawn browser sessions; isRunning
// reports whether the instance's browser session is up.
func New(dial DialFunc, isRunning func(ctx context.Context, instanceID uint) bool) *Manager {
	return &Manager{
		dial:              dial,
		isRunning:         isRunning,
		store:             dbStore{},
		ReconcileInterval: 30 * time.Second,
		SuperviseInterval: 30 * time.Second,
		sessions:          make(map[uint]*instanceSession),
		baseCtx:           context.Background(),
	}
}

// SetStore overrides persistence (tests).
func (m *Manager) SetStore(s Store) { m.store = s }

// Run supervises sessions until ctx is canceled: it (re)starts managers for
// running browsers that have an authenticator configured — covering control
// plane restarts and sessions spawned before the setting was enabled.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.baseCtx = ctx
	m.mu.Unlock()
	t := time.NewTicker(m.SuperviseInterval)
	defer t.Stop()
	m.superviseOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-t.C:
			m.superviseOnce(ctx)
		}
	}
}

func (m *Manager) superviseOnce(ctx context.Context) {
	ids, err := m.store.RunningInstancesWithAuthenticator()
	if err != nil {
		log.Printf("browserauthn: list running instances: %v", err)
		return
	}
	for _, id := range ids {
		m.mu.Lock()
		_, active := m.sessions[id]
		m.mu.Unlock()
		if !active {
			go func(id uint) {
				if err := m.Start(ctx, id); err != nil {
					log.Printf("browserauthn: supervise start instance=%d: %v", id, err)
				}
			}(id)
		}
	}
}

// Start ensures an authenticator session for the instance per its current
// setting and waits (bounded) until existing tabs are configured. It is the
// browserprov session-ready hook, so it runs before the session is reported
// running — the agent's first CDP command already sees the authenticator.
// Returns nil (and stops any session) when the setting is off.
func (m *Manager) Start(ctx context.Context, instanceID uint) error {
	setting, err := m.store.InstanceSetting(instanceID)
	if err != nil {
		return err
	}
	kinds := KindsFor(setting)

	m.mu.Lock()
	existing := m.sessions[instanceID]
	if existing != nil && sameKinds(existing.kinds, kinds) && !existing.isStopped() {
		m.mu.Unlock()
		return existing.waitReady(ctx, 20*time.Second)
	}
	if existing != nil {
		delete(m.sessions, instanceID)
	}
	if len(kinds) == 0 {
		m.mu.Unlock()
		if existing != nil {
			existing.stopAndWait()
		}
		return nil
	}
	sctx, cancel := context.WithCancel(m.baseCtx)
	s := &instanceSession{
		m:          m,
		instanceID: instanceID,
		kinds:      kinds,
		cancel:     cancel,
		ready:      make(chan struct{}),
		stopped:    make(chan struct{}),
		exited:     make(chan struct{}),
		targets:    make(map[string]*target),
	}
	m.sessions[instanceID] = s
	m.mu.Unlock()
	if existing != nil {
		existing.stopAndWait()
	}

	go s.run(sctx)
	return s.waitReady(ctx, 20*time.Second)
}

// Stop tears down the instance's authenticator session (if any).
func (m *Manager) Stop(instanceID uint) {
	m.mu.Lock()
	s := m.sessions[instanceID]
	delete(m.sessions, instanceID)
	m.mu.Unlock()
	if s != nil {
		s.stopAndWait()
	}
}

// Reload re-applies the instance's setting (e.g. after it changed). Only
// acts on running browsers; otherwise the next spawn picks it up.
func (m *Manager) Reload(ctx context.Context, instanceID uint) error {
	if !m.isRunning(ctx, instanceID) {
		m.Stop(instanceID)
		return nil
	}
	return m.Start(ctx, instanceID)
}

// RemoveCredential deletes a credential from every live tab (the DB row is
// deleted by the caller).
func (m *Manager) RemoveCredential(ctx context.Context, instanceID uint, credentialID string) {
	m.mu.Lock()
	s := m.sessions[instanceID]
	m.mu.Unlock()
	if s != nil {
		s.removeEverywhere(ctx, credentialID, "")
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	all := m.sessions
	m.sessions = make(map[uint]*instanceSession)
	m.mu.Unlock()
	for _, s := range all {
		s.stopAndWait()
	}
}

func (m *Manager) forget(s *instanceSession) {
	m.mu.Lock()
	if m.sessions[s.instanceID] == s {
		delete(m.sessions, s.instanceID)
	}
	m.mu.Unlock()
}

func sameKinds(a, b []Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// target is one attached page with its virtual authenticators.
type target struct {
	sessionID string
	auths     map[Kind]string // kind → authenticatorId
}

// instanceSession is the long-lived CDP connection for one browser.
type instanceSession struct {
	m          *Manager
	instanceID uint
	kinds      []Kind
	cancel     context.CancelFunc

	readyOnce sync.Once
	ready     chan struct{}
	stopOnce  sync.Once
	stopped   chan struct{}
	exited    chan struct{} // closed when run() returns

	// mu serialises tab setup against credential fan-out so a tab being set
	// up can never miss a credential created concurrently in another tab.
	mu      sync.Mutex
	conn    *cdpConn
	targets map[string]*target // CDP sessionId → target
}

func (s *instanceSession) markReady() { s.readyOnce.Do(func() { close(s.ready) }) }

func (s *instanceSession) isStopped() bool {
	select {
	case <-s.stopped:
		return true
	default:
		return false
	}
}

func (s *instanceSession) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		close(s.stopped)
	})
	s.markReady()
}

// stopAndWait stops the session and waits (bounded) for its goroutine to
// finish its final reconcile.
func (s *instanceSession) stopAndWait() {
	s.stop()
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
	}
}

func (s *instanceSession) waitReady(ctx context.Context, timeout time.Duration) error {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return errors.New("virtual authenticator setup timed out")
	}
}

// run keeps a CDP connection up while the browser session is running,
// reinstalling everything on every (re)connect.
func (s *instanceSession) run(ctx context.Context) {
	defer close(s.exited)
	defer s.m.forget(s)
	defer s.stop()
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if !s.m.isRunning(ctx, s.instanceID) {
			// Browser session ended (reaped/stopped). The next spawn calls
			// Start again; nothing to keep alive meanwhile.
			return
		}
		if err != nil {
			log.Printf("browserauthn: instance=%d: %v (retrying in %s)", s.instanceID, err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *instanceSession) connectAndServe(ctx context.Context) error {
	conn, err := dialCDP(ctx, s.m.dial, s.instanceID)
	if err != nil {
		return err
	}
	defer conn.Close()

	s.mu.Lock()
	s.conn = conn
	s.targets = make(map[string]*target)
	s.mu.Unlock()

	// Event loop first so attach events produced by setAutoAttach (existing
	// tabs) are consumed while we wait for the command response.
	evDone := make(chan struct{})
	go func() {
		defer close(evDone)
		s.eventLoop(ctx, conn)
	}()

	// Count pre-existing tabs so readiness waits until each one has its
	// authenticator (they attach without pausing, unlike new tabs).
	var existing struct {
		TargetInfos []struct {
			Type string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := conn.Call(ctx, "", "Target.getTargets", nil, &existing); err != nil {
		return fmt.Errorf("Target.getTargets: %w", err)
	}
	expected := 0
	for _, t := range existing.TargetInfos {
		if t.Type == "page" {
			expected++
		}
	}
	if err := conn.Call(ctx, "", "Target.setAutoAttach", map[string]interface{}{
		"autoAttach":             true,
		"waitForDebuggerOnStart": true,
		"flatten":                true,
		"filter":                 []map[string]interface{}{{"type": "page"}},
	}, nil); err != nil {
		return fmt.Errorf("Target.setAutoAttach: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.targets)
		s.mu.Unlock()
		if n >= expected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.markReady()
	log.Printf("browserauthn: instance=%d virtual authenticator active (%v)", s.instanceID, s.kinds)

	ticker := time.NewTicker(s.m.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final sweep before the connection closes.
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.reconcile(fctx)
			cancel()
			return nil
		case <-evDone:
			return errors.New("cdp connection closed")
		case <-ticker.C:
			s.reconcile(ctx)
		}
	}
}

func (s *instanceSession) eventLoop(ctx context.Context, conn *cdpConn) {
	for ev := range conn.Events() {
		switch ev.Method {
		case "Target.attachedToTarget":
			var p struct {
				SessionID          string `json:"sessionId"`
				WaitingForDebugger bool   `json:"waitingForDebugger"`
				TargetInfo         struct {
					Type string `json:"type"`
				} `json:"targetInfo"`
			}
			if json.Unmarshal(ev.Params, &p) != nil {
				continue
			}
			go s.setupTarget(ctx, conn, p.SessionID, p.WaitingForDebugger)
		case "Target.detachedFromTarget":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(ev.Params, &p) == nil {
				s.mu.Lock()
				delete(s.targets, p.SessionID)
				s.mu.Unlock()
			}
		case "WebAuthn.credentialAdded", "WebAuthn.credentialUpdated":
			var p struct {
				AuthenticatorID string     `json:"authenticatorId"`
				Credential      credential `json:"credential"`
			}
			if json.Unmarshal(ev.Params, &p) == nil {
				s.onCredentialStored(ctx, ev.SessionID, p.AuthenticatorID, p.Credential)
			}
		case "WebAuthn.credentialAsserted":
			var p struct {
				AuthenticatorID string     `json:"authenticatorId"`
				Credential      credential `json:"credential"`
			}
			if json.Unmarshal(ev.Params, &p) == nil {
				s.onCredentialAsserted(ctx, ev.SessionID, p.AuthenticatorID, p.Credential)
			}
		case "WebAuthn.credentialDeleted":
			var p struct {
				AuthenticatorID string `json:"authenticatorId"`
				CredentialID    string `json:"credentialId"`
			}
			if json.Unmarshal(ev.Params, &p) == nil {
				if err := s.m.store.DeleteByCredential(s.instanceID, p.CredentialID); err != nil {
					log.Printf("browserauthn: instance=%d delete credential: %v", s.instanceID, err)
				}
				s.removeEverywhere(ctx, p.CredentialID, ev.SessionID)
			}
		}
	}
}

// setupTarget installs authenticators + stored credentials into a freshly
// attached page, then resumes it.
func (s *instanceSession) setupTarget(ctx context.Context, conn *cdpConn, sessionID string, waiting bool) {
	s.mu.Lock()
	err := s.installLocked(ctx, conn, sessionID)
	s.mu.Unlock()
	if err != nil {
		log.Printf("browserauthn: instance=%d setup tab: %v", s.instanceID, err)
	}
	// Always resume, even on failure, so a broken setup never hangs a tab.
	if waiting {
		if err := conn.Call(ctx, sessionID, "Runtime.runIfWaitingForDebugger", nil, nil); err != nil {
			log.Printf("browserauthn: instance=%d resume tab: %v", s.instanceID, err)
		}
	}
}

func (s *instanceSession) installLocked(ctx context.Context, conn *cdpConn, sessionID string) error {
	if err := conn.Call(ctx, sessionID, "WebAuthn.enable", map[string]interface{}{"enableUI": false}, nil); err != nil {
		return fmt.Errorf("WebAuthn.enable: %w", err)
	}
	rows, err := s.m.store.List(s.instanceID)
	if err != nil {
		return fmt.Errorf("list passkeys: %w", err)
	}
	t := &target{sessionID: sessionID, auths: make(map[Kind]string)}
	for _, k := range s.kinds {
		var res struct {
			AuthenticatorID string `json:"authenticatorId"`
		}
		if err := conn.Call(ctx, sessionID, "WebAuthn.addVirtualAuthenticator",
			map[string]interface{}{"options": authenticatorOptions(k)}, &res); err != nil {
			return fmt.Errorf("addVirtualAuthenticator(%s): %w", k, err)
		}
		t.auths[k] = res.AuthenticatorID
		for _, r := range rows {
			if Kind(r.Authenticator) != k {
				continue
			}
			c, err := fromRow(r)
			if err != nil {
				log.Printf("browserauthn: instance=%d passkey %d: %v", s.instanceID, r.ID, err)
				continue
			}
			if err := addCredential(ctx, conn, sessionID, res.AuthenticatorID, c); err != nil {
				log.Printf("browserauthn: instance=%d inject passkey %d: %v", s.instanceID, r.ID, err)
			}
		}
	}
	s.targets[sessionID] = t
	return nil
}

func addCredential(ctx context.Context, conn *cdpConn, sessionID, authID string, c credential) error {
	return conn.Call(ctx, sessionID, "WebAuthn.addCredential", map[string]interface{}{
		"authenticatorId": authID,
		"credential":      c,
	}, nil)
}

// kindOf resolves which kind an authenticator in a tab belongs to.
func (s *instanceSession) kindOfLocked(sessionID, authID string) (Kind, bool) {
	t := s.targets[sessionID]
	if t == nil {
		return "", false
	}
	for k, id := range t.auths {
		if id == authID {
			return k, true
		}
	}
	return "", false
}

// onCredentialStored persists a newly registered (or updated) credential and
// copies it into every other tab.
func (s *instanceSession) onCredentialStored(ctx context.Context, sessionID, authID string, c credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.kindOfLocked(sessionID, authID)
	if !ok {
		return
	}
	row, err := toRow(s.instanceID, k, c)
	if err == nil {
		now := time.Now().UTC()
		row.LastUsedAt = &now
		err = s.m.store.Upsert(row)
	}
	if err != nil {
		log.Printf("browserauthn: instance=%d persist credential: %v", s.instanceID, err)
	}
	s.replaceInOthersLocked(ctx, sessionID, k, c)
}

// onCredentialAsserted advances the persisted sign count and refreshes the
// other tabs so their copy never signs with a stale (lower) counter.
func (s *instanceSession) onCredentialAsserted(ctx context.Context, sessionID, authID string, c credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.kindOfLocked(sessionID, authID)
	if !ok {
		return
	}
	if err := s.m.store.RecordUse(s.instanceID, c.CredentialID, c.SignCount); err != nil {
		// Unknown credential (e.g. missed credentialAdded) — persist it now.
		if row, err2 := toRow(s.instanceID, k, c); err2 == nil {
			now := time.Now().UTC()
			row.LastUsedAt = &now
			if err3 := s.m.store.Upsert(row); err3 != nil {
				log.Printf("browserauthn: instance=%d persist asserted credential: %v", s.instanceID, err3)
			}
		}
	}
	s.replaceInOthersLocked(ctx, sessionID, k, c)
}

func (s *instanceSession) replaceInOthersLocked(ctx context.Context, srcSession string, k Kind, c credential) {
	if c.PrivateKey == "" {
		return
	}
	for sid, t := range s.targets {
		if sid == srcSession {
			continue
		}
		authID, ok := t.auths[k]
		if !ok {
			continue
		}
		_ = s.conn.Call(ctx, sid, "WebAuthn.removeCredential", map[string]interface{}{
			"authenticatorId": authID, "credentialId": c.CredentialID,
		}, nil)
		if err := addCredential(ctx, s.conn, sid, authID, c); err != nil {
			log.Printf("browserauthn: instance=%d fan-out credential: %v", s.instanceID, err)
		}
	}
}

func (s *instanceSession) removeEverywhere(ctx context.Context, credentialID, exceptSession string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return
	}
	for sid, t := range s.targets {
		if sid == exceptSession {
			continue
		}
		for _, authID := range t.auths {
			_ = s.conn.Call(ctx, sid, "WebAuthn.removeCredential", map[string]interface{}{
				"authenticatorId": authID, "credentialId": credentialID,
			}, nil)
		}
	}
}

// reconcile pulls every credential from every tab and persists anything the
// DB doesn't know yet or whose sign count moved forward — a safety net for
// missed events.
func (s *instanceSession) reconcile(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return
	}
	rows, err := s.m.store.List(s.instanceID)
	if err != nil {
		return
	}
	known := make(map[string]uint32, len(rows))
	for _, r := range rows {
		known[r.CredentialID] = r.SignCount
	}
	for sid, t := range s.targets {
		for k, authID := range t.auths {
			var res struct {
				Credentials []credential `json:"credentials"`
			}
			if err := s.conn.Call(ctx, sid, "WebAuthn.getCredentials",
				map[string]interface{}{"authenticatorId": authID}, &res); err != nil {
				continue
			}
			for _, c := range res.Credentials {
				n, ok := known[c.CredentialID]
				if ok && c.SignCount <= n {
					continue
				}
				if ok {
					_ = s.m.store.RecordUse(s.instanceID, c.CredentialID, c.SignCount)
				} else if row, err := toRow(s.instanceID, k, c); err == nil {
					if err := s.m.store.Upsert(row); err != nil {
						log.Printf("browserauthn: instance=%d reconcile persist: %v", s.instanceID, err)
						continue
					}
				}
				known[c.CredentialID] = c.SignCount
			}
		}
	}
}
