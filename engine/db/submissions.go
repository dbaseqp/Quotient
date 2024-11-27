package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type SubmissionSchema struct {
	TeamID             uint
	InjectID           uint
	SubmissionTime     time.Time
	SubmissionFileName string
	Version            int
	Team               TeamSchema `gorm:"foreignKey:TeamID"`
}

func GetSubmissionsForInject(injectID uint) ([]SubmissionSchema, error) {
	var submissions []SubmissionSchema
	result := db.Table("submission_schemas").Preload("Team", func(db *gorm.DB) *gorm.DB {
		return db.Select("id", "name") // only select the id and name fields from the team
	}).Where("inject_id = ?", injectID).Find(&submissions)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return submissions, nil
		}
		return nil, result.Error
	}
	return submissions, nil
}

// gorm hook to set the version of the submission
func (submission *SubmissionSchema) BeforeCreate(tx *gorm.DB) error {
	var existingSubmission SubmissionSchema
	result := tx.Where("inject_id = ? AND team_id = ?", submission.InjectID, submission.TeamID).Order("version desc").First(&existingSubmission)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	}

	if result.Error == nil {
		submission.Version = existingSubmission.Version + 1
	} else {
		submission.Version = 1
	}

	return nil
}

func CreateSubmission(submission SubmissionSchema) (SubmissionSchema, error) {
	result := db.Create(&submission)
	if result.Error != nil {
		return SubmissionSchema{}, result.Error
	}
	return submission, nil
}
