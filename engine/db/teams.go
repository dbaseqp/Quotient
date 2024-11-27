package db

import (
	"errors"

	"gorm.io/gorm"
)

type TeamSchema struct {
	ID                uint
	Name              string                   `gorm:"unique"` // https://www.postgresql.org/docs/current/functions-sequence.html#:~:text=Caution,of%20assigned%20values
	Identifier        string                   // can't use unique bc empty string as distinct not supported // used for things like IP calculation
	Token             string                   // koth
	Active            bool                     // idr what this is for
	Checks            []ServiceCheckSchema     `gorm:"foreignKey:TeamID"` // get checks who belong to this team
	ManualAdjustments []ManualAdjustmentSchema `gorm:"foreignKey:TeamID"` // get adjustments who belong to this team
	SLAs              []SLASchema              `gorm:"foreignKey:TeamID"` // get slas who belong to this team
	SubmissionData    []SubmissionSchema       `gorm:"foreignKey:TeamID"` // get inject submissions who belong to this team
}

func CreateTeam(team TeamSchema) (TeamSchema, error) {
	result := db.Table("team_schemas").Create(&team)
	if result.Error != nil {
		return TeamSchema{}, result.Error
	}
	return team, nil
}

func GetTeams() ([]TeamSchema, error) {
	var teams []TeamSchema
	result := db.Table("team_schemas").Order("id").Find(&teams)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return teams, nil
		} else {
			return nil, result.Error
		}
	}
	return teams, nil
}

func GetTeamByUsername(name string) (TeamSchema, error) {
	var team TeamSchema
	result := db.Table("team_schemas").Where("name = ?", name).First(&team)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return team, nil
		} else {
			return TeamSchema{}, result.Error
		}
	}
	return team, nil
}

func GetTeamSummary(teamID uint) ([]string, map[string]int, []RoundSchema, error) {
	namePerService := []string{}
	slaCountPerService := make(map[string]int)
	last10RoundsPerService := []RoundSchema{}

	// get services names
	if result := db.Table("service_check_schemas").Select("DISTINCT(service_name)").Where("team_id = ?", teamID).Find(&namePerService); result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return namePerService, slaCountPerService, last10RoundsPerService, nil
		} else {
			return nil, nil, nil, result.Error
		}
	}

	// get uptime per service for this team

	// get sla counts per service for this team
	for _, name := range namePerService {
		slaCountPerService[name] = 0
	}

	rows, err := db.Raw("SELECT service_name, count(*) FROM sla_schemas WHERE team_id = ? GROUP BY service_name", teamID).Rows()
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count int
		rows.Scan(&name, &count)
		slaCountPerService[name] = count
	}

	// get last 10 rounds for this team
	if result := db.Table("round_schemas").Preload("Checks", "team_id = ?", teamID).Order("id desc").Limit(10).Find(&last10RoundsPerService); result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return namePerService, slaCountPerService, last10RoundsPerService, nil
		} else {
			return nil, nil, nil, result.Error
		}
	}

	return namePerService, slaCountPerService, last10RoundsPerService, nil
}

func UpdateTeam(teamID uint, identifier string, active bool) error {
	result := db.Table("team_schemas").Where("id = ?", teamID).Updates(map[string]interface{}{"identifier": identifier, "active": active})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func GetTeamScore(teamID uint) (int, int, int, error) {
	// get service points
	servicePoints := 0
	rows, err := db.Raw("SELECT SUM(points) FROM service_check_schemas WHERE team_id = ?", teamID).Rows()
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		rows.Scan(&servicePoints)
	}

	// get sla violations
	var slas []SLASchema
	if result := db.Table("sla_schemas").Where("team_id = ?", teamID).Find(&slas); result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return servicePoints, 0, servicePoints, nil
		} else {
			return 0, 0, 0, result.Error
		}
	}

	slaPoints := 0
	for _, sla := range slas {
		slaPoints += sla.Penalty
	}

	return servicePoints, len(slas), slaPoints, nil
}
