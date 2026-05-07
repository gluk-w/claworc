package database

import (
	"errors"

	"gorm.io/gorm"
)

// GetInstance fetches an Instance by ID.
func GetInstance(id uint) (*Instance, error) {
	var inst Instance
	if err := DB.First(&inst, id).Error; err != nil {
		return nil, err
	}
	return &inst, nil
}

// Team role constants. Stored as the Role column on TeamMember.
const (
	TeamRoleUser    = "user"
	TeamRoleManager = "manager"
)

func ListTeams() ([]Team, error) {
	var teams []Team
	if err := DB.Order("name asc").Find(&teams).Error; err != nil {
		return nil, err
	}
	return teams, nil
}

func GetTeam(id uint) (*Team, error) {
	var t Team
	if err := DB.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func CreateTeam(t *Team) error {
	return DB.Create(t).Error
}

func UpdateTeam(id uint, updates map[string]interface{}) error {
	return DB.Model(&Team{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteTeam removes a team along with its memberships and provider
// whitelist. A team with instances still attached is rejected — the
// caller must reassign or delete those instances first.
func DeleteTeam(id uint) error {
	if _, err := GetTeam(id); err != nil {
		return err
	}
	var instCount int64
	DB.Model(&Instance{}).Where("team_id = ?", id).Count(&instCount)
	if instCount > 0 {
		return errors.New("team has instances; reassign or delete them first")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id = ?", id).Delete(&TeamMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where("team_id = ?", id).Delete(&TeamProvider{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Team{}, id).Error
	})
}

// TeamMembership pairs a team with the caller's role, used for /api/me.
type TeamMembership struct {
	Team
	Role string `json:"role"`
}

// GetUserTeams returns the teams a user belongs to with their per-team role.
func GetUserTeams(userID uint) ([]TeamMembership, error) {
	var rows []struct {
		Team
		Role string
	}
	err := DB.Table("teams").
		Select("teams.*, team_members.role as role").
		Joins("JOIN team_members ON team_members.team_id = teams.id").
		Where("team_members.user_id = ?", userID).
		Order("teams.name asc").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]TeamMembership, len(rows))
	for i, r := range rows {
		out[i] = TeamMembership{Team: r.Team, Role: r.Role}
	}
	return out, nil
}

// GetTeamRole returns the user's role in the given team, or "" if the user
// is not a member.
func GetTeamRole(userID, teamID uint) string {
	var m TeamMember
	err := DB.Where("team_id = ? AND user_id = ?", teamID, userID).First(&m).Error
	if err != nil {
		return ""
	}
	return m.Role
}

// IsTeamManager reports whether a user has manager role on the given team.
func IsTeamManager(userID, teamID uint) bool {
	return GetTeamRole(userID, teamID) == TeamRoleManager
}

// UserManagedTeamIDs returns the IDs of teams the user manages.
func UserManagedTeamIDs(userID uint) ([]uint, error) {
	var ids []uint
	err := DB.Model(&TeamMember{}).
		Where("user_id = ? AND role = ?", userID, TeamRoleManager).
		Pluck("team_id", &ids).Error
	return ids, err
}

// UserTeamIDs returns the IDs of every team the user belongs to.
func UserTeamIDs(userID uint) ([]uint, error) {
	var ids []uint
	err := DB.Model(&TeamMember{}).
		Where("user_id = ?", userID).
		Pluck("team_id", &ids).Error
	return ids, err
}

// TeamMemberWithUser is a denormalized membership row with the user's
// username and global role attached, for the team admin UI.
type TeamMemberWithUser struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	UserRole string `json:"user_role"` // global role: admin|user
}

func ListTeamMembers(teamID uint) ([]TeamMemberWithUser, error) {
	var out []TeamMemberWithUser
	err := DB.Table("team_members").
		Select("team_members.user_id, users.username, team_members.role, users.role as user_role").
		Joins("JOIN users ON users.id = team_members.user_id").
		Where("team_members.team_id = ?", teamID).
		Order("users.username asc").
		Scan(&out).Error
	return out, err
}

// SetTeamMember upserts a team membership with the given role. Pass an
// empty role to remove the membership.
func SetTeamMember(teamID, userID uint, role string) error {
	if role == "" {
		return DB.Where("team_id = ? AND user_id = ?", teamID, userID).Delete(&TeamMember{}).Error
	}
	if role != TeamRoleUser && role != TeamRoleManager {
		return errors.New("invalid team role")
	}
	var existing TeamMember
	err := DB.Where("team_id = ? AND user_id = ?", teamID, userID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DB.Create(&TeamMember{TeamID: teamID, UserID: userID, Role: role}).Error
	}
	if err != nil {
		return err
	}
	return DB.Model(&existing).Update("role", role).Error
}

// GetTeamProviderIDs returns the IDs of global LLM providers whitelisted
// for the team.
func GetTeamProviderIDs(teamID uint) ([]uint, error) {
	var ids []uint
	err := DB.Model(&TeamProvider{}).
		Where("team_id = ?", teamID).
		Pluck("provider_id", &ids).Error
	return ids, err
}

// SetTeamProviders replaces the team's provider whitelist.
func SetTeamProviders(teamID uint, providerIDs []uint) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id = ?", teamID).Delete(&TeamProvider{}).Error; err != nil {
			return err
		}
		for _, pid := range providerIDs {
			if err := tx.Create(&TeamProvider{TeamID: teamID, ProviderID: pid}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// IsProviderAllowedForTeam reports whether a global provider is whitelisted
// for the given team.
func IsProviderAllowedForTeam(teamID, providerID uint) bool {
	var count int64
	DB.Model(&TeamProvider{}).
		Where("team_id = ? AND provider_id = ?", teamID, providerID).
		Count(&count)
	return count > 0
}

// AccessibleInstanceIDs returns the union of instance IDs a user can see.
//
//   - Admins are not handled here (callers should bypass this and list all
//     instances directly).
//   - Managers see every instance in any team they manage.
//   - Regular users see only instances explicitly granted via UserInstance,
//     restricted to teams they are a member of.
func AccessibleInstanceIDs(userID uint) ([]uint, error) {
	memberships, err := GetUserTeams(userID)
	if err != nil {
		return nil, err
	}
	var managedTeamIDs []uint
	var memberTeamIDs []uint
	for _, m := range memberships {
		if m.Role == TeamRoleManager {
			managedTeamIDs = append(managedTeamIDs, m.ID)
		} else {
			memberTeamIDs = append(memberTeamIDs, m.ID)
		}
	}

	idSet := make(map[uint]struct{})

	if len(managedTeamIDs) > 0 {
		var ids []uint
		if err := DB.Model(&Instance{}).
			Where("team_id IN ?", managedTeamIDs).
			Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			idSet[id] = struct{}{}
		}
	}

	if len(memberTeamIDs) > 0 {
		var ids []uint
		if err := DB.Table("user_instances").
			Select("user_instances.instance_id").
			Joins("JOIN instances ON instances.id = user_instances.instance_id").
			Where("user_instances.user_id = ? AND instances.team_id IN ?", userID, memberTeamIDs).
			Pluck("instance_id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			idSet[id] = struct{}{}
		}
	}

	out := make([]uint, 0, len(idSet))
	for id := range idSet {
		out = append(out, id)
	}
	return out, nil
}
