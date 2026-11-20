package db

import "gorm.io/gorm"

type SLASchema struct {
	TeamID      uint
	RoundID     uint
	Round       RoundSchema
	ServiceName string
	Penalty     int
}

func (d *DB) CreateSLA(sla SLASchema) (SLASchema, error) {
	result := d.db.Table("sla_schemas").Create(&sla)
	if result.Error != nil {
		return SLASchema{}, result.Error
	}
	return sla, nil
}

// CreateRoundWithSLAs stores a scored round, its checks and the SLA
// violations it triggered in one transaction, so a failed write leaves none
// of them behind and the round can be retried under the same ID.
func (d *DB) CreateRoundWithSLAs(round RoundSchema, slas []SLASchema) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("round_schemas").Create(&round).Error; err != nil {
			return err
		}
		if len(slas) == 0 {
			return nil
		}
		return tx.Table("sla_schemas").Create(&slas).Error
	})
}
