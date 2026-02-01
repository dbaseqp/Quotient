package db

import (
	"errors"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type InjectSchema struct {
	ID              uint
	Title           string `gorm:"unique"` // also used as directory name
	Description     string
	OpenTime        time.Time
	DueTime         time.Time
	CloseTime       time.Time
	InjectFileNames pq.StringArray     `gorm:"type:text[]"`
	Submissions     []SubmissionSchema `gorm:"foreignKey:InjectID"`
}

// CreateInject creates a new inject in the database using the provided schema
func CreateInject(inject InjectSchema) (InjectSchema, error) {
	result := db.Table("inject_schemas").Create(&inject)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

// GetInjects retrieves all injects from the database
func GetInjects() ([]InjectSchema, error) {
	var injects []InjectSchema
	result := db.Table("inject_schemas").Order("open_time desc, id desc").Find(&injects)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return injects, nil
		}
		return nil, result.Error
	}
	return injects, nil
}

// GetInjectByID retrieves a single inject by ID
func GetInjectByID(id uint) (InjectSchema, error) {
	var inject InjectSchema
	result := db.Table("inject_schemas").First(&inject, id)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

// UpdateInject
func UpdateInject(inject InjectSchema) (InjectSchema, error) {
	result := db.Table("inject_schemas").Save(&inject)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

// DeleteInject deletes an inject and its submissions from the database
func DeleteInject(inject InjectSchema) error {
	// Delete submissions first (foreign key constraint)
	if err := db.Table("submission_schemas").Where("inject_id = ?", inject.ID).Delete(&SubmissionSchema{}).Error; err != nil {
		return err
	}
	result := db.Table("inject_schemas").Delete(&inject)
	if result.Error != nil {
		return result.Error
	}
	return nil
}
