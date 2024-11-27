package db

import (
	"errors"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AnnouncementSchema struct {
	ID                    uint
	Title                 string `gorm:"unique"` // also used as directory name
	Description           string
	OpenTime              time.Time
	AnnouncementFileNames pq.StringArray `gorm:"type:text[]"`
}

// CreateAnnouncement creates a new announcement in the database using the provided schema
func CreateAnnouncement(announcement AnnouncementSchema) (AnnouncementSchema, error) {
	result := db.Table("announcement_schemas").Create(&announcement)
	if result.Error != nil {
		return AnnouncementSchema{}, result.Error
	}
	return announcement, nil
}

// GetAnnouncements retrieves all announcements from the database
func GetAnnouncements() ([]AnnouncementSchema, error) {
	var announcements []AnnouncementSchema
	result := db.Table("announcement_schemas").Order("open_time desc, id desc").Find(&announcements)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return announcements, nil
		}
		return nil, result.Error
	}
	return announcements, nil
}

// delete an announcement from the database
func DeleteAnnouncement(announcement AnnouncementSchema) error {
	result := db.Table("announcement_schemas").Delete(&announcement)
	if result.Error != nil {
		return result.Error
	}
	return nil
}
