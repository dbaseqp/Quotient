package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoundSchema represents a database schema for a round, including its checks and SLAs.
type RoundSchema struct {
	ID        uint
	StartTime time.Time
	Checks    []ServiceCheckSchema `gorm:"foreignKey:RoundID"`
	SLAs      []SLASchema          `gorm:"foreignKey:RoundID"`
}

// BeforeCreate is a GORM hook that ensures no duplicate records are created
// by adding an ON CONFLICT DO NOTHING clause to the SQL statement.
func (check *ServiceCheckSchema) BeforeCreate(tx *gorm.DB) (err error) {
	cols := []clause.Column{}
	colsNames := []string{}
	for _, field := range tx.Statement.Schema.PrimaryFields {
		cols = append(cols, clause.Column{Name: field.DBName})
		colsNames = append(colsNames, field.DBName)
	}
	tx.Statement.AddClause(clause.OnConflict{
		Columns: cols,
		// DoUpdates: clause.AssignmentColumns(colsNames),
		DoNothing: true,
	})
	return nil
}

// CreateRound inserts a new round into the database and returns the created round.
// If an error occurs during the insertion, it is returned.
func CreateRound(round RoundSchema) (RoundSchema, error) {
	result := db.Table("round_schemas").Create(&round)
	if result.Error != nil {
		return RoundSchema{}, result.Error
	}
	return round, nil
}

// GetLastRound retrieves the most recent round from the database, including its associated checks.
// If no round is found, it returns an empty RoundSchema and no error.
func GetLastRound() (RoundSchema, error) {
	var round RoundSchema
	result := db.Table("round_schemas").Preload("Checks").Order("id desc").First(&round)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return round, nil
		}
		return RoundSchema{}, result.Error
	}
	return round, nil
}
