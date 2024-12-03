package db

import (
	"errors"

	"gorm.io/gorm"
)

// AccessLevel represents the level of access a user or entity has.
type AccessLevel int

const (
	// NONE represents no access level for users or entities.
	NONE AccessLevel = iota
	// USER represents a standard user access level.
	USER
	// ADMIN represents an administrative access level.
	ADMIN
)

// AttackSchema represents an instance of a vector against a team.
type AttackSchema struct {
	BoxID          uint
	TeamID         uint
	Narrative      string
	EvidenceImages []string `gorm:"type:text[]"` // /submissions/red/teamID/boxID/image.png
	Vulnerable     bool
	AccessLevel    int

	DataAccessPII                 bool
	DataAccessPassword            bool
	DataAccessSystemConfiguration bool
	DataAccessDatabase            bool
}

// GetAttacks retrieves all attack schemas from the database.
func GetAttacks() ([]AttackSchema, error) {
	var attacks []AttackSchema
	result := db.Table("attack_schemas").Find(&attacks)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return attacks, nil
		}
		return nil, result.Error
	}
	return attacks, nil
}

// CreateAttack creates a new attack schema in the database.
func CreateAttack(attack AttackSchema) (AttackSchema, error) {
	result := db.Table("attack_schemas").Create(&attack)
	if result.Error != nil {
		return AttackSchema{}, result.Error
	}
	return attack, nil
}
