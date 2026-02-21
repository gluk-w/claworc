// Package sshkeys handles SSH key generation, storage, rotation, and verification
// for the control-plane-to-agent authentication model.
//
// Each agent instance gets a dedicated ED25519 key pair. The control plane stores
// the private key on disk (at DefaultKeyDir/{instanceName}.key with 0600 permissions)
// and deploys the public key to the agent's /root/.ssh/authorized_keys file during
// instance creation.
//
// # Key Lifecycle
//
// 1. Generation: [GenerateKeyPair] creates an ED25519 key pair and returns the
// public key in SSH authorized_keys format and the private key in PEM format.
//
// 2. Storage: [SavePrivateKey] writes the private key to disk with restricted
// permissions (0600). The directory is created with 0700 permissions if it
// does not exist.
//
// 3. Rotation: [RotateKeyPair] performs zero-downtime key rotation by generating
// a new key pair, appending the new public key to the agent's authorized_keys,
// verifying connectivity with the new key, and then removing the old key.
// If any step fails, the operation is rolled back to avoid locking out the
// instance.
//
// 4. Verification: [GetPublicKeyFingerprint] and [VerifyFingerprint] provide
// fingerprint-based integrity checks. [MakeHostKeyCallback] implements
// Trust On First Use (TOFU) for host key verification.
//
// # Security Model
//
//   - Private keys are stored with 0600 permissions; key directories use 0700.
//   - ED25519 is used exclusively (no RSA/DSA/ECDSA) for its strong security
//     properties and small key size.
//   - Key rotation is atomic: either both the new key is deployed and the old
//     key is removed, or neither change persists.
//   - Fingerprint verification detects potential MITM attacks or key tampering.
//   - Log messages sanitize file paths and instance names to prevent log injection.
//
// # Usage
//
//	// Generate a new key pair for an instance
//	pubKey, privKey, err := sshkeys.GenerateKeyPair()
//	if err != nil { ... }
//
//	// Save the private key to disk
//	keyPath, err := sshkeys.SavePrivateKey("my-instance", privKey)
//	if err != nil { ... }
//
//	// Rotate keys for a running instance (zero-downtime)
//	newPub, newKeyPath, result, err := sshkeys.RotateKeyPair(
//	    sshClient, "my-instance", string(oldPubKey), host, port,
//	)
//
//	// Verify a key's fingerprint
//	err = sshkeys.VerifyFingerprint(pubKey, expectedFingerprint)
//	if err != nil {
//	    if mismatch, ok := err.(*sshkeys.FingerprintMismatchError); ok {
//	        // Handle fingerprint mismatch (possible tampering)
//	    }
//	}
package sshkeys
