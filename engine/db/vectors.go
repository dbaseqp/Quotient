package db

import (
	"errors"

	"gorm.io/gorm"
)

// a generalized implementation of some type of vuln against a specific box
type VectorSchema struct {
	ID                        uint
	VulnID                    uint
	BoxID                     uint
	Port                      int
	Protocol                  string
	ImplementationDescription string
}

func (d *DB) GetVectors() ([]VectorSchema, error) {
	var vectors []VectorSchema
	result := d.db.Table("vector_schemas").Order("port asc").Find(&vectors)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vectors, nil
		} else {
			return nil, result.Error
		}
	}
	return vectors, nil
}

func (d *DB) CreateVector(vector VectorSchema) (VectorSchema, error) {
	result := d.db.Table("vector_schemas").Create(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}

func (d *DB) UpdateVector(vector VectorSchema) (VectorSchema, error) {
	result := d.db.Table("vector_schemas").Save(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}
