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

func (d *DB) GetBoxes() ([]BoxSchema, error) {
	var boxes []BoxSchema
	result := d.db.Table("box_schemas").Preload("Vectors").Find(&boxes)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return boxes, nil
		} else {
			return nil, result.Error
		}
	}
	return boxes, nil
}

func (d *DB) CreateBox(box BoxSchema) (BoxSchema, error) {
	result := d.db.Table("box_schemas").Create(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}

func (d *DB) UpdateBox(box BoxSchema) (BoxSchema, error) {
	result := d.db.Table("box_schemas").Save(&box)
	if result.Error != nil {
		return BoxSchema{}, result.Error
	}
	return box, nil
}
