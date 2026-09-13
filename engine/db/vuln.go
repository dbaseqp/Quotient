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

func (d *DB) GetVulns() ([]VulnSchema, error) {
	var vulns []VulnSchema
	result := d.db.Table("vuln_schemas").Find(&vulns)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return vulns, nil
		} else {
			return nil, result.Error
		}
	}
	return vulns, nil
}

func (d *DB) CreateVuln(vuln VulnSchema) (VulnSchema, error) {
	result := d.db.Table("vuln_schemas").Create(&vuln)
	if result.Error != nil {
		return VulnSchema{}, result.Error
	}
	return vuln, nil
}
