package db

type SLASchema struct {
	TeamID      uint
	RoundID     uint
	Round       RoundSchema
	ServiceName string
	Penalty     int
}

func CreateSLA(sla SLASchema) (SLASchema, error) {
	result := db.Table("sla_schemas").Create(&sla)
	if result.Error != nil {
		return SLASchema{}, result.Error
	}
	return sla, nil
}
