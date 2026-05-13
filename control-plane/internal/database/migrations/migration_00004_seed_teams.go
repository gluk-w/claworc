package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00004_seed_teams: data backfill on top of 00003_create_teams.sql.
//
// Ensures a "Default Team" exists, backfills any instance with
// team_id=0, mirrors existing UserInstance grants into
// TeamMember(role=user), and promotes users whose legacy
// can_create_instances flag was set to manager of the Default team.
// Safe to re-run.
func init() {
	register(&goose.Migration{
		Version: 4,
		Source:  "00004_seed_teams.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			_ = tx
			return seedTeamsAndBackfill(DB())
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("seed_teams migration is not reversible")
		},
	})
}

// seedTeamsAndBackfill creates a "Default Team" only when the teams
// table is empty, backfills any instance with team_id=0 to point at the
// first team, mirrors UserInstance rows into TeamMember(role=user), and
// promotes users with can_create_instances=true to manager of that team.
// Idempotent.
//
// The teams/team_members/team_providers tables are created by the
// sibling SQL migration 00003_create_teams.sql. The instances.team_id
// column is added here via AutoMigrate so it's idempotent: on fresh
// installs the baseline already created it from the current Instance
// model (no-op); on upgrades the baseline is stamped, leaving this
// AutoMigrate call to add the column.
func seedTeamsAndBackfill(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(&models.Instance{}); err != nil {
		return fmt.Errorf("auto-migrate instances.team_id: %w", err)
	}

	var teamCount int64
	if err := gdb.Model(&models.Team{}).Count(&teamCount).Error; err != nil {
		return fmt.Errorf("count teams: %w", err)
	}
	var defaultTeam models.Team
	if teamCount == 0 {
		defaultTeam = models.Team{Name: "Default Team", Description: "Default team"}
		if err := gdb.Create(&defaultTeam).Error; err != nil {
			return fmt.Errorf("seed default team: %w", err)
		}
	} else {
		if err := gdb.Order("id asc").First(&defaultTeam).Error; err != nil {
			return fmt.Errorf("load anchor team: %w", err)
		}
	}

	if err := gdb.Model(&models.Instance{}).Where("team_id IS NULL OR team_id = 0").
		Update("team_id", defaultTeam.ID).Error; err != nil {
		return fmt.Errorf("backfill instance.team_id: %w", err)
	}

	var grants []models.UserInstance
	if err := gdb.Find(&grants).Error; err != nil {
		return fmt.Errorf("load user_instances: %w", err)
	}
	for _, g := range grants {
		var inst models.Instance
		if err := gdb.First(&inst, g.InstanceID).Error; err != nil {
			continue
		}
		teamID := inst.TeamID
		if teamID == 0 {
			teamID = defaultTeam.ID
		}
		var existing int64
		gdb.Model(&models.TeamMember{}).Where("team_id = ? AND user_id = ?", teamID, g.UserID).Count(&existing)
		if existing == 0 {
			gdb.Create(&models.TeamMember{TeamID: teamID, UserID: g.UserID, Role: "user"})
		}
	}

	var creators []models.User
	if err := gdb.Where("can_create_instances = ?", true).Find(&creators).Error; err != nil {
		return fmt.Errorf("load creators: %w", err)
	}
	for _, u := range creators {
		var existing models.TeamMember
		err := gdb.Where("team_id = ? AND user_id = ?", defaultTeam.ID, u.ID).First(&existing).Error
		if err != nil {
			gdb.Create(&models.TeamMember{TeamID: defaultTeam.ID, UserID: u.ID, Role: "manager"})
		} else if existing.Role != "manager" {
			gdb.Model(&existing).Update("role", "manager")
		}
	}

	return nil
}
