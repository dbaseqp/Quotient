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

func GetVectors() ([]VectorSchema, error) {
	var vectors []VectorSchema
	result := db.Table("vector_schemas").Order("port asc").Find(&vectors)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vectors, nil
		} else {
			return nil, result.Error
		}
	}
	return vectors, nil
}

func CreateVector(vector VectorSchema) (VectorSchema, error) {
	result := db.Table("vector_schemas").Create(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}

func UpdateVector(vector VectorSchema) (VectorSchema, error) {
	result := db.Table("vector_schemas").Save(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}
