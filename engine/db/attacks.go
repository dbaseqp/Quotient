package db

import (
	"errors"

	"gorm.io/gorm"
)

type AccessLevel int

const (
	NONE AccessLevel = iota
	USER
	ADMIN
)

// a specific instance of a vector against a team
type AttackSchema struct {
	ID             uint
	VectorID       uint
	Vector         VectorSchema
	TeamID         uint
	Narrative      string
	EvidenceImages []AttackImageSchema `gorm:"type:text[]"` // /submissions/red/teamID/boxID/image.png
	AccessLevel    int

	StillWorks                    bool
	DataAccessPII                 bool
	DataAccessPassword            bool
	DataAccessSystemConfiguration bool
	DataAccessDatabase            bool
}

type AttackImageSchema struct {
	AttackBoxID  uint   `gorm:"primaryKey"`
	AttackTeamID uint   `gorm:"primaryKey"`
	URI          string `gorm:"primaryKey"`
}

func GetAttacks() ([]AttackSchema, error) {
	var attacks []AttackSchema
	result := db.Table("attack_schemas").Find(&attacks)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return attacks, nil
		} else {
			return nil, result.Error
		}
	}
	return attacks, nil
}

func CreateAttack(attack AttackSchema) (AttackSchema, error) {
	result := db.Table("attack_schemas").Create(&attack)
	if result.Error != nil {
		return AttackSchema{}, result.Error
	}
	return attack, nil
}

func UpdateAttack(attack AttackSchema) (AttackSchema, error) {
	result := db.Table("attack_schemas").Save(&attack)
	if result.Error != nil {
		return AttackSchema{}, result.Error
	}
	return attack, nil
}
