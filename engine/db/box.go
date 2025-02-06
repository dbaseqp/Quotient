package db

import (
	"errors"

	"gorm.io/gorm"
)

type BoxSchema struct {
	ID       uint
	IP       string `gorm:"unique"`
	Hostname string
	Vectors  []VectorSchema `gorm:"foreignKey:BoxID"`
}

func GetBoxes() ([]BoxSchema, error) {
	var boxes []BoxSchema
	result := db.Table("box_schemas").Preload("Vectors").Find(&boxes)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return boxes, nil
		} else {
			return nil, result.Error
		}
	}
	return boxes, nil
}

func CreateBox(box BoxSchema) (BoxSchema, error) {
	result := db.Table("box_schemas").Create(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}

func UpdateBox(box BoxSchema) (BoxSchema, error) {
	result := db.Table("box_schemas").Save(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}
