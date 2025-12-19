package db

import "gorm.io/gorm"

type CompetitionStateSchema struct {
	ID      uint `gorm:"primarykey"`
	Started bool
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
	var state CompetitionStateSchema
	result := db.First(&state)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			state.Started = started
			return db.Create(&state).Error
		}
		return result.Error
	}

	state.Started = started
	return db.Save(&state).Error
}
