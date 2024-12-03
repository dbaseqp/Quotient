package db

import (
	"errors"

	"gorm.io/gorm"
)

// Status represents the state of a vulnerability or related entity.
type Status int

// VulnSchema represents a vulnerability instance against a system.
type VulnSchema struct {
	ID          uint
	Name        string
	Description string
	Vectors     []VectorSchema `gorm:"foreignKey:VulnID"`
}

// GetVulns retrieves all vulnerability records from the database.
func GetVulns() ([]VulnSchema, error) {
	var vulns []VulnSchema
	result := db.Table("vuln_schemas").Find(&vulns)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vulns, nil
		}
		return nil, result.Error
	}
	return vulns, nil
}

// CreateVuln adds a new vulnerability record to the database.
func CreateVuln(vuln VulnSchema) (VulnSchema, error) {
	result := db.Table("vuln_schemas").Create(&vuln)
	if result.Error != nil {
		return VulnSchema{}, result.Error
	}
	return vuln, nil
}
