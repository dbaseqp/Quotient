package db

import (
	"errors"

	"gorm.io/gorm"
)

type Status int

// class of vulnerability
type VulnSchema struct {
	ID          uint
	Name        string
	Description string
	// Vectors     []VectorSchema `gorm:"foreignKey:VulnID"`
}

func GetVulns() ([]VulnSchema, error) {
	var vulns []VulnSchema
	result := db.Table("vuln_schemas").Find(&vulns)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vulns, nil
		} else {
			return nil, result.Error
		}
	}
	return vulns, nil
}

func CreateVuln(vuln VulnSchema) (VulnSchema, error) {
	result := db.Table("vuln_schemas").Create(&vuln)
	if result.Error != nil {
		return VulnSchema{}, result.Error
	}
	return vuln, nil
}
