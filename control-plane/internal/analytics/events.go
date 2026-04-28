package analytics

const (
	EventInstanceCreated       = "instance_created"
	EventInstanceDeleted       = "instance_deleted"
	EventSkillUploaded         = "skill_uploaded"
	EventSkillDeleted          = "skill_deleted"
	EventSharedFolderCreated   = "shared_folder_created"
	EventSharedFolderDeleted   = "shared_folder_deleted"
	EventBackupScheduleCreated = "backup_schedule_created"
	EventBackupCreatedManual   = "backup_created_manual"
	EventUserCreated           = "user_created"
	EventUserUpdated           = "user_updated"
	EventUserDeleted           = "user_deleted"
	EventPasswordChanged       = "password_changed"
	EventProviderAdded         = "provider_added"
	EventProviderDeleted       = "provider_deleted"
	EventSSHKeyRotated         = "ssh_key_rotated"
	EventGlobalEnvVarsEdited   = "global_env_vars_edited"
	EventInstanceEnvVarsEdited = "instance_env_vars_edited"
	EventOptOut                = "opt_out"
)

// AllowedEvents is the set of events accepted by the collector. Mirrors the
// allowlist that the Cloudflare Worker enforces server-side.
var AllowedEvents = map[string]bool{
	EventInstanceCreated:       true,
	EventInstanceDeleted:       true,
	EventSkillUploaded:         true,
	EventSkillDeleted:          true,
	EventSharedFolderCreated:   true,
	EventSharedFolderDeleted:   true,
	EventBackupScheduleCreated: true,
	EventBackupCreatedManual:   true,
	EventUserCreated:           true,
	EventUserUpdated:           true,
	EventUserDeleted:           true,
	EventPasswordChanged:       true,
	EventProviderAdded:         true,
	EventProviderDeleted:       true,
	EventSSHKeyRotated:         true,
	EventGlobalEnvVarsEdited:   true,
	EventInstanceEnvVarsEdited: true,
	EventOptOut:                true,
}
