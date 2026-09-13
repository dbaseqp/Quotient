package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type CompetitionStateSchema struct {
	ID        uint `gorm:"primarykey"`
	Started   bool
	StartedAt *time.Time
}

func GetCompetitionStarted() bool {
	var state CompetitionStateSchema
	result := db.First(&state)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return false
		}
		return false
	}
	return state.Started
}

func SetCompetitionStarted(started bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var state CompetitionStateSchema
		result := tx.First(&state)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}

		firstStart := started && state.StartedAt == nil
		state.Started = started
		if firstStart {
			now := time.Now()
			state.StartedAt = &now
		}

		if result.Error != nil {
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&state).Error; err != nil {
			return err
		}

		if firstStart {
			return recalculateImportedInjectTimes(tx, *state.StartedAt)
		}
		return nil
	})
}

func GetCompetitionStart() (*time.Time, error) {
	var state CompetitionStateSchema
	result := db.First(&state)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return state.StartedAt, nil
}
