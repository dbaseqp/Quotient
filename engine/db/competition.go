package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CompetitionStateSchema struct {
	ID        uint `gorm:"primarykey"`
	Started   bool
	StartedAt *time.Time
	// OvertimeEnd is the configured end an admin resumed scoring after.
	OvertimeEnd *time.Time
}

func (d *DB) GetCompetitionStarted() bool {
	var state CompetitionStateSchema
	result := d.db.First(&state)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return false
		}
		return false
	}
	return state.Started
}

func (d *DB) SetCompetitionStarted(started bool) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		var state CompetitionStateSchema
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}

		firstStart := started && state.StartedAt == nil
		state.Started = started
		if firstStart {
			now := time.Now()
			state.StartedAt = &now
		}

		state.ID = 1
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"started", "started_at"}),
		}).Create(&state).Error; err != nil {
			return err
		}

		if firstStart {
			return recalculateImportedInjectTimes(tx, *state.StartedAt)
		}
		return nil
	})
}

func (d *DB) GetCompetitionStart() (*time.Time, error) {
	var state CompetitionStateSchema
	result := d.db.First(&state)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return state.StartedAt, nil
}

func (d *DB) GetOvertimeEnd() (time.Time, error) {
	var state CompetitionStateSchema
	result := d.db.First(&state)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return time.Time{}, nil
	}
	if result.Error != nil || state.OvertimeEnd == nil {
		return time.Time{}, result.Error
	}
	return *state.OvertimeEnd, nil
}

func (d *DB) SetOvertimeEnd(end time.Time) error {
	return d.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"overtime_end"}),
	}).Create(&CompetitionStateSchema{ID: 1, OvertimeEnd: &end}).Error
}
