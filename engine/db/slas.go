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

func GetSLAs() ([]SLASchema, error) {
	var slas []SLASchema
	result := db.Table("sla_schemas").Find(&slas)
	if result.Error != nil {
		return nil, result.Error
	}
	return slas, nil
}
