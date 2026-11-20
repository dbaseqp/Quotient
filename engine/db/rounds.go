package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RoundSchema struct {
	ID        uint
	StartTime time.Time
	Checks    []ServiceCheckSchema `gorm:"foreignKey:RoundID"`
	SLAs      []SLASchema          `gorm:"foreignKey:RoundID"`
}

// this is so when we create a new round, we can add checks to it
func (check *ServiceCheckSchema) BeforeCreate(tx *gorm.DB) (err error) {
	cols := []clause.Column{}
	// colsNames := []string{}
	for _, field := range tx.Statement.Schema.PrimaryFields {
		cols = append(cols, clause.Column{Name: field.DBName})
		// colsNames = append(colsNames, field.DBName)
	}
	tx.Statement.AddClause(clause.OnConflict{
		Columns: cols,
		// DoUpdates: clause.AssignmentColumns(colsNames),
		DoNothing: true,
	})
	return nil
}

// CreateRound stores a round, its checks and its SLA violations in one transaction.
// SLAs are inserted separately: gorm's association upsert emits ON CONFLICT with no target, which Postgres rejects.
func (d *DB) CreateRound(round RoundSchema) (RoundSchema, error) {
	err := d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("round_schemas").Omit("SLAs").Create(&round).Error; err != nil {
			return err
		}
		if len(round.SLAs) == 0 {
			return nil
		}
		return tx.Table("sla_schemas").Create(&round.SLAs).Error
	})
	if err != nil {
		return RoundSchema{}, err
	}
	return round, nil
}

func (d *DB) GetLastRound() (RoundSchema, error) {
	var round RoundSchema
	result := d.db.Table("round_schemas").Preload("Checks").Order("id desc").First(&round)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return round, nil
		}
		return RoundSchema{}, result.Error
	}
	return round, nil
}

// HasScoredRound reports whether any scored round has been stored.
func (d *DB) HasScoredRound() (bool, error) {
	var n int64
	err := d.db.Table("round_schemas").Limit(1).Count(&n).Error
	return n > 0, err
}

func (d *DB) RefreshScoresMaterializedView() error {
	// Use concurrent refresh to avoid blocking reads
	return d.db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY cumulative_scores").Error
}
