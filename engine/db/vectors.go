package db

import (
	"errors"

	"gorm.io/gorm"
)

// VectorSchema represents the schema for a vector in the database.
type VectorSchema struct {
	VulnID                    uint
	BoxID                     uint
	Port                      int
	Endpoint                  string
	ImplementationDescription string
}

// GetVectors retrieves all vectors from the database.
func GetVectors() ([]VectorSchema, error) {
	var vectors []VectorSchema
	result := db.Table("vector_schemas").Find(&vectors)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vectors, nil
		}
		return nil, result.Error
	}
	return vectors, nil
}

// CreateVector creates a new vector in the database.
func CreateVector(vector VectorSchema) (VectorSchema, error) {
	result := db.Table("vector_schemas").Create(&vector)
	if result.Error != nil {
		return VectorSchema{}, result.Error
	}
	return vector, nil
}
