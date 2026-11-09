package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Preflight rounds are kept apart from round_schemas so they never count toward points, uptime or SLAs.
type PreflightRoundSchema struct {
	ID        uint
	StartTime time.Time
	Checks    []PreflightCheckSchema `gorm:"foreignKey:RoundID"`
}

type PreflightCheckSchema struct {
	ID          uint `gorm:"primarykey"`
	RoundID     uint `gorm:"index"`
	TeamID      uint
	Team        TeamSchema
	ServiceName string
	ServiceType string
	Result      bool
	Error       string
	Debug       string
}

func (d *DB) CreatePreflightRound(round PreflightRoundSchema) error {
	return d.db.Create(&round).Error
}

// GetLastPreflightRoundID is 0 when no preflight round has been stored.
func (d *DB) GetLastPreflightRoundID() (uint, error) {
	var last uint
	err := d.db.Model(&PreflightRoundSchema{}).Select("COALESCE(MAX(id), 0)").Scan(&last).Error
	return last, err
}

// GetLatestPreflightRound returns the zero round when none has been stored.
func (d *DB) GetLatestPreflightRound() (PreflightRoundSchema, error) {
	var round PreflightRoundSchema
	err := d.db.
		Preload("Checks", func(tx *gorm.DB) *gorm.DB { return tx.Order("team_id, service_name") }).
		Preload("Checks.Team").
		Order("id desc").First(&round).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PreflightRoundSchema{}, nil
	}
	return round, err
}
