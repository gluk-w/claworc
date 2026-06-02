package database

import (
	"fmt"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// dbHostKeyStore implements sshproxy.HostKeyStore backed by the main SQLite DB.
type dbHostKeyStore struct{}

// NewHostKeyStore returns a HostKeyStore backed by the control-plane database.
func NewHostKeyStore() sshproxy.HostKeyStore {
	return &dbHostKeyStore{}
}

func (s *dbHostKeyStore) LoadAll() (map[uint][]byte, error) {
	var rows []models.SSHHostKey
	if err := DB.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load ssh host keys: %w", err)
	}
	out := make(map[uint][]byte, len(rows))
	for _, r := range rows {
		out[r.InstanceID] = r.PublicKey
	}
	return out, nil
}

func (s *dbHostKeyStore) Save(instanceID uint, pubkeyBytes []byte) error {
	row := models.SSHHostKey{
		InstanceID: instanceID,
		PublicKey:  pubkeyBytes,
		StoredAt:   time.Now(),
	}
	return DB.Save(&row).Error
}

func (s *dbHostKeyStore) Delete(instanceID uint) error {
	return DB.Delete(&models.SSHHostKey{}, instanceID).Error
}
