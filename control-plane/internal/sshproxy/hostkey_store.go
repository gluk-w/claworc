package sshproxy

// HostKeyStore persists SSH host keys across platform restarts.
// It is optional — SSHManager works without one (in-memory TOFU only).
// Implement this interface to survive restarts without re-trusting every bot.
type HostKeyStore interface {
	// LoadAll returns all stored keys as instanceID → raw SSH wire-format bytes.
	LoadAll() (map[uint][]byte, error)
	// Save persists a host key for the given instance.
	Save(instanceID uint, pubkeyBytes []byte) error
	// Delete removes the stored key for the given instance.
	Delete(instanceID uint) error
}
