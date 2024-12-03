package db

// SLASchema represents the SLA (Service Level Agreement) data structure.
type SLASchema struct {
	TeamID      uint
	RoundID     uint
	Round       RoundSchema
	ServiceName string
	Penalty     int
}

// CreateSLA creates a new SLA record in the database.
// It takes an SLASchema object as input and returns the created SLASchema or an error.
func CreateSLA(sla SLASchema) (SLASchema, error) {
	result := db.Table("sla_schemas").Create(&sla)
	if result.Error != nil {
		return SLASchema{}, result.Error
	}
	return sla, nil
}
