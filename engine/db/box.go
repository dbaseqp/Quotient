package db

import (
	"errors"
	"quotient/engine/config"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BoxSchema struct {
	ID       uint
	IP       string        `gorm:"unique"`
	Ports    pq.Int32Array `gorm:"type:int[]"`
	Hostname string
	Vectors  []VectorSchema `gorm:"foreignKey:BoxID"`
	Attacks  []AttackSchema `gorm:"foreignKey:BoxID"`
}

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

func GetBoxes() ([]BoxSchema, error) {
	var boxes []BoxSchema
	result := db.Table("box_schemas").Find(&boxes)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return boxes, nil
		} else {
			return nil, result.Error
		}
	}
	return boxes, nil
}

// Sets the default value for Ports to an empty slice so that it is not nil
func (box *BoxSchema) AfterFind(*gorm.DB) error {
	if box.Ports == nil {
		box.Ports = []int32{}
	}
	return nil
}

// update a box
func UpdateBox(box BoxSchema) (BoxSchema, error) {
	result := db.Table("box_schemas").Save(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}
