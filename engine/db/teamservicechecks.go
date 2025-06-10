package db

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TeamServiceCheckSchema stores per-team enable state for each service
// combination of TeamID and ServiceName should be unique
// Enabled defaults to true when no row exists
type TeamServiceCheckSchema struct {
	ID          uint   `gorm:"primaryKey"`
	TeamID      uint   `gorm:"uniqueIndex:idx_team_service"`
	ServiceName string `gorm:"uniqueIndex:idx_team_service"`
	Enabled     bool
}

// IsTeamServiceEnabled returns true if the service check is enabled for a team.
// If no entry exists, it defaults to true.
func IsTeamServiceEnabled(teamID uint, serviceName string) (bool, error) {
	var t TeamServiceCheckSchema
	result := db.Table("team_service_check_schemas").Where("team_id = ? AND service_name = ?", teamID, serviceName).First(&t)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return true, nil
		}
		return true, result.Error
	}
	return t.Enabled, nil
}

// SetTeamServiceEnabled creates or updates the enabled state for a team/service
func SetTeamServiceEnabled(teamID uint, serviceName string, enabled bool) error {
	t := TeamServiceCheckSchema{TeamID: teamID, ServiceName: serviceName, Enabled: enabled}
	return db.Table("team_service_check_schemas").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "team_id"}, {Name: "service_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled"}),
	}).Create(&t).Error
}

// GetAllTeamServiceChecks returns all per-team service check entries
func GetAllTeamServiceChecks() ([]TeamServiceCheckSchema, error) {
	var out []TeamServiceCheckSchema
	result := db.Table("team_service_check_schemas").Find(&out)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, result.Error
	}
	return out, nil
}
