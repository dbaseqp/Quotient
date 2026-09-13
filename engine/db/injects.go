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
	OpenOffset      *int64             `json:"-"`
	DueOffset       *int64             `json:"-"`
	CloseOffset     *int64             `json:"-"`
}

// CreateInject creates a new inject in the database using the provided schema
func (d *DB) CreateInject(inject InjectSchema) (InjectSchema, error) {
	result := d.db.Table("inject_schemas").Create(&inject)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

func (d *DB) CreateInjectBatch(injects []InjectSchema) ([]InjectSchema, error) {
	err := d.db.Transaction(func(tx *gorm.DB) error {
		return tx.Table("inject_schemas").Create(&injects).Error
	})
	if err != nil {
		return nil, err
	}
	return injects, nil
}

func (d *DB) DeleteInjectBatch(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("submission_schemas").Where("inject_id IN ?", ids).Delete(&SubmissionSchema{}).Error; err != nil {
			return err
		}
		return tx.Table("inject_schemas").Where("id IN ?", ids).Delete(&InjectSchema{}).Error
	})
}

func (d *DB) RecalculateImportedInjectTimes(start time.Time) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		return recalculateImportedInjectTimes(tx, start)
	})
}

func recalculateImportedInjectTimes(tx *gorm.DB, start time.Time) error {
	var injects []InjectSchema
	if err := tx.Table("inject_schemas").Where("open_offset IS NOT NULL").Find(&injects).Error; err != nil {
		return err
	}
	for i := range injects {
		injects[i].OpenTime = start.Add(time.Duration(*injects[i].OpenOffset) * time.Second)
		injects[i].DueTime = start.Add(time.Duration(*injects[i].DueOffset) * time.Second)
		injects[i].CloseTime = start.Add(time.Duration(*injects[i].CloseOffset) * time.Second)
		if err := tx.Table("inject_schemas").Save(&injects[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetInjects retrieves all injects from the database
func (d *DB) GetInjects() ([]InjectSchema, error) {
	var injects []InjectSchema
	result := d.db.Table("inject_schemas").Order("open_time desc, id desc").Find(&injects)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return injects, nil
		}
		return nil, result.Error
	}
	return injects, nil
}

// GetInjectByID retrieves a single inject by ID
func (d *DB) GetInjectByID(id uint) (InjectSchema, error) {
	var inject InjectSchema
	result := d.db.Table("inject_schemas").First(&inject, id)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

// UpdateInject
func (d *DB) UpdateInject(inject InjectSchema) (InjectSchema, error) {
	result := d.db.Table("inject_schemas").Save(&inject)
	if result.Error != nil {
		return InjectSchema{}, result.Error
	}
	return inject, nil
}

// DeleteInject deletes an inject and its submissions from the database
func (d *DB) DeleteInject(inject InjectSchema) error {
	// Delete submissions first (foreign key constraint)
	if err := d.db.Table("submission_schemas").Where("inject_id = ?", inject.ID).Delete(&SubmissionSchema{}).Error; err != nil {
		return err
	}
	result := d.db.Table("inject_schemas").Delete(&inject)
	if result.Error != nil {
		return result.Error
	}
	return nil
}
