package db

import (
	"errors"

	"gorm.io/gorm"
)

type VectorSchema struct {
	VulnID                    uint
	BoxID                     uint
	Port                      int
	Endpoint                  string
	ImplementationDescription string
}

func GetVectors() ([]VectorSchema, error) {
	var vectors []VectorSchema
	result := db.Table("vector_schemas").Find(&vectors)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vectors, nil
		} else {
			return nil, result.Error
		}
	}
	return vectors, nil
}

// create a new vector
func CreateVector(vector VectorSchema) (VectorSchema, error) {
	result := db.Table("vector_schemas").Create(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}
