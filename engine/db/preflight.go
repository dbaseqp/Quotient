package db

import (
	"time"
)

// Preflight rounds are kept apart from round_schemas so they never count toward points, uptime or SLAs.
type PreflightCheckSchema struct {
	ID          uint `gorm:"primarykey"`
	RoundID     uint `gorm:"index"`
	StartTime   time.Time
	TeamID      uint
	Team        TeamSchema
	ServiceName string
	ServiceType string
	Result      bool
	Error       string
	Debug       string
}

func (d *DB) CreatePreflightChecks(checks []PreflightCheckSchema) error {
	return d.db.Create(&checks).Error
}

func (d *DB) GetLastPreflightRound() (uint, error) {
	var last uint
	err := d.db.Model(&PreflightCheckSchema{}).Select("COALESCE(MAX(round_id), 0)").Scan(&last).Error
	return last, err
}

func (d *DB) GetLatestPreflightChecks() ([]PreflightCheckSchema, error) {
	var checks []PreflightCheckSchema
	err := d.db.Preload("Team").
		Where("round_id = (?)", d.db.Model(&PreflightCheckSchema{}).Select("MAX(round_id)")).
		Order("team_id, service_name").
		Find(&checks).Error
	return checks, err
}
