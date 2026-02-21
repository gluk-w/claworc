package sshaudit

import (
	"sync"

	"gorm.io/gorm"
)

var (
	globalAuditor *Auditor
	registryMu    sync.RWMutex
)

// InitGlobal creates and stores the global Auditor instance.
// Call this once during application startup after the database is initialized.
func InitGlobal(db *gorm.DB, retentionDays int) {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalAuditor = NewAuditor(db, retentionDays)
}

// GetAuditor returns the global Auditor instance.
func GetAuditor() *Auditor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return globalAuditor
}

// SetGlobalForTest sets the global Auditor for tests.
func SetGlobalForTest(a *Auditor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalAuditor = a
}

// ResetGlobalForTest clears the global Auditor.
func ResetGlobalForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	globalAuditor = nil
}
