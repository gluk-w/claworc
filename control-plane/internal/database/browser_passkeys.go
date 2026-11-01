package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BrowserPasskey helpers. Callers (internal/browserauthn) are responsible for
// encrypting PrivateKey/LargeBlob before writing and decrypting after reading;
// this layer stores the ciphertext verbatim (the database package cannot
// import internal/utils without an import cycle).

// ListBrowserPasskeys returns every stored passkey for an instance, oldest
// first so re-injection order is stable.
func ListBrowserPasskeys(instanceID uint) ([]BrowserPasskey, error) {
	var rows []BrowserPasskey
	err := DB.Where("instance_id = ?", instanceID).Order("id ASC").Find(&rows).Error
	return rows, err
}

// GetBrowserPasskey returns one passkey scoped to its instance.
func GetBrowserPasskey(instanceID, id uint) (*BrowserPasskey, error) {
	var p BrowserPasskey
	if err := DB.Where("instance_id = ? AND id = ?", instanceID, id).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertBrowserPasskey inserts the passkey or, when (instance_id,
// credential_id) already exists, refreshes its mutable fields. SignCount is
// only ever moved forward so a stale write can never regress it.
func UpsertBrowserPasskey(p *BrowserPasskey) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var existing BrowserPasskey
		err := tx.Where("instance_id = ? AND credential_id = ?", p.InstanceID, p.CredentialID).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(p).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]interface{}{
			"rp_id":              p.RpID,
			"user_handle":        p.UserHandle,
			"user_name":          p.UserName,
			"user_display_name":  p.UserDisplayName,
			"is_resident":        p.IsResident,
			"private_key":        p.PrivateKey,
			"backup_eligibility": p.BackupEligibility,
			"backup_state":       p.BackupState,
			"large_blob":         p.LargeBlob,
		}
		if p.SignCount > existing.SignCount {
			updates["sign_count"] = p.SignCount
		}
		if p.LastUsedAt != nil {
			updates["last_used_at"] = p.LastUsedAt
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		p.ID = existing.ID
		p.CreatedAt = existing.CreatedAt
		return nil
	})
}

// RecordBrowserPasskeyUse advances the sign count (never backwards) and bumps
// last_used_at. Returns gorm.ErrRecordNotFound when the credential is unknown.
func RecordBrowserPasskeyUse(instanceID uint, credentialID string, signCount uint32) error {
	res := DB.Model(&BrowserPasskey{}).
		Where("instance_id = ? AND credential_id = ?", instanceID, credentialID).
		Updates(map[string]interface{}{
			"sign_count":   gorm.Expr("CASE WHEN sign_count < ? THEN ? ELSE sign_count END", signCount, signCount),
			"last_used_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteBrowserPasskeyByCredential removes a passkey by its credential ID.
func DeleteBrowserPasskeyByCredential(instanceID uint, credentialID string) error {
	return DB.Where("instance_id = ? AND credential_id = ?", instanceID, credentialID).
		Delete(&BrowserPasskey{}).Error
}

// DeleteBrowserPasskey removes a passkey by row ID, scoped to its instance.
func DeleteBrowserPasskey(instanceID, id uint) error {
	return DB.Where("instance_id = ? AND id = ?", instanceID, id).Delete(&BrowserPasskey{}).Error
}

// DeleteBrowserPasskeysForInstance removes every passkey of an instance.
// Called when the instance is deleted.
func DeleteBrowserPasskeysForInstance(instanceID uint) error {
	return DB.Where("instance_id = ?", instanceID).Delete(&BrowserPasskey{}).Error
}

// CloneBrowserPasskeys copies every passkey of src onto dst (ciphertext is
// copied as-is; both instances share the same Fernet key). Existing dst
// credentials with the same ID are left untouched.
func CloneBrowserPasskeys(srcInstanceID, dstInstanceID uint) error {
	rows, err := ListBrowserPasskeys(srcInstanceID)
	if err != nil || len(rows) == 0 {
		return err
	}
	for i := range rows {
		rows[i].ID = 0
		rows[i].InstanceID = dstInstanceID
		rows[i].CreatedAt = time.Time{}
	}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}
