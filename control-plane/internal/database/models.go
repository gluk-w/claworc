package database

import "github.com/gluk-w/claworc/control-plane/internal/database/models"

// Type aliases re-export every model type from the models subpackage so
// that callers using `database.Instance{}` etc. keep working. The
// canonical definitions live in internal/database/models — split out so
// that the internal/database/migrations subpackage can reference model
// types via the GORM Migrator without an import cycle.

type (
	Skill              = models.Skill
	Instance           = models.Instance
	Team               = models.Team
	TeamMember         = models.TeamMember
	TeamProvider       = models.TeamProvider
	BrowserSession     = models.BrowserSession
	ProviderModel      = models.ProviderModel
	ProviderModelCost  = models.ProviderModelCost
	LLMProvider        = models.LLMProvider
	LLMGatewayKey      = models.LLMGatewayKey
	LLMRequestLog      = models.LLMRequestLog
	Setting            = models.Setting
	User               = models.User
	UserInstance       = models.UserInstance
	Backup             = models.Backup
	BackupSchedule     = models.BackupSchedule
	SharedFolder       = models.SharedFolder
	KanbanBoard        = models.KanbanBoard
	KanbanTask         = models.KanbanTask
	KanbanComment      = models.KanbanComment
	KanbanArtifact     = models.KanbanArtifact
	InstanceSoul       = models.InstanceSoul
	WebAuthnCredential = models.WebAuthnCredential
)

// Helper re-exports keep `database.ParseTeamIDs(...)` etc. working for
// existing callers. The implementations live in the models package.

func IsLegacyEmbedded(image string) bool { return models.IsLegacyEmbedded(image) }

func ParseProviderModels(raw string) []ProviderModel { return models.ParseProviderModels(raw) }

func ParseSharedFolderInstanceIDs(raw string) []uint {
	return models.ParseSharedFolderInstanceIDs(raw)
}

func EncodeSharedFolderInstanceIDs(ids []uint) string {
	return models.EncodeSharedFolderInstanceIDs(ids)
}

func ParseTeamIDs(raw string) []uint { return models.ParseTeamIDs(raw) }

func EncodeTeamIDs(ids []uint) string { return models.EncodeTeamIDs(ids) }
