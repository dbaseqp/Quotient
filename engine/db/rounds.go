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

func CreateRound(round RoundSchema) (RoundSchema, error) {
	result := db.Table("round_schemas").Create(&round)
	if result.Error != nil {
		return RoundSchema{}, result.Error
	}
	return round, nil
}

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

func RefreshScoresMaterializedView() error {
	return db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY cumulative_scores").Error
}
