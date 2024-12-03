package db

import (
	"errors"
	"quotient/engine/config"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BoxSchema represents the schema for a box in the database.
type BoxSchema struct {
	ID       uint
	IP       string        `gorm:"unique"`
	Ports    pq.Int32Array `gorm:"type:int[]"`
	Hostname string
	Vectors  []VectorSchema `gorm:"foreignKey:BoxID"`
	Attacks  []AttackSchema `gorm:"foreignKey:BoxID"`
}

// LoadBoxes loads box configurations into the database.
// It uses a transaction to ensure atomicity and avoids duplicate entries with OnConflict.
func LoadBoxes(config *config.ConfigSettings) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, box := range config.Box {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&BoxSchema{IP: box.IP}).Error; err != nil {
				return err
			}
		}
		return nil
	})

	// for formatting reasons
	if err != nil {
		return err
	}
	return nil
}

// GetBoxes retrieves all box records from the database.
// Returns a slice of BoxSchema and an error if any occurs during the query.
func GetBoxes() ([]BoxSchema, error) {
	var boxes []BoxSchema
	result := db.Table("box_schemas").Find(&boxes)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return boxes, nil
		}
		return nil, result.Error
	}
	return boxes, nil
}

// AfterFind sets the default value for Ports to an empty slice so that it is not nil
func (box *BoxSchema) AfterFind(*gorm.DB) error {
	if box.Ports == nil {
		box.Ports = []int32{}
	}
	return nil
}

// UpdateBox updates a box record in the database
func UpdateBox(box BoxSchema) (BoxSchema, error) {
	result := db.Table("box_schemas").Save(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}
