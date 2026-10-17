package db

type SLASchema struct {
	TeamID      uint
	RoundID     uint
	Round       RoundSchema
	ServiceName string
	Penalty     int
	// WaivedPenalty is what the SLA would have cost when Penalty is 0
	WaivedPenalty int
}

func (d *DB) CreateSLA(sla SLASchema) (SLASchema, error) {
	result := d.db.Table("sla_schemas").Create(&sla)
	if result.Error != nil {
		return SLASchema{}, result.Error
	}
	return sla, nil
}
