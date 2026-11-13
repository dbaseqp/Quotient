package db

import (
	"errors"
	"log/slog"

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

func (d *DB) CreateTeam(team TeamSchema) (TeamSchema, error) {
	result := d.db.Table("team_schemas").Create(&team)
	if result.Error != nil {
		return TeamSchema{}, result.Error
	}
	return team, nil
}

func (d *DB) GetTeams() ([]TeamSchema, error) {
	var teams []TeamSchema
	result := d.db.Table("team_schemas").Order("id").Find(&teams)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return teams, nil
		} else {
			return nil, result.Error
		}
	}
	return teams, nil
}

type ServiceSummary struct {
	ServiceName  string        `json:"ServiceName"`
	SlaCount     int           `json:"SlaCount"`
	Last10Rounds []RoundSchema `json:"Last10Rounds"`
	Uptime       float64       `json:"Uptime"`
}

func (d *DB) GetTeamSummary(teamID uint) ([]ServiceSummary, error) {
	var serviceSummaries []ServiceSummary
	namePerService := []string{}

	if result := d.db.Table("service_check_schemas").Select("DISTINCT(service_name)").Where("team_id = ?", teamID).Find(&namePerService); result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return serviceSummaries, nil
		}
		return nil, result.Error
	}

	for _, name := range namePerService {
		var c int64
		if result := d.db.Table("sla_schemas").Where("team_id = ? AND service_name = ?", teamID, name).Count(&c); result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, result.Error
		}

		var last10Rounds []RoundSchema
		if result := d.db.Table("round_schemas").Preload("Checks", "team_id = ? AND service_name = ?", teamID, name).Order("id desc").Limit(10).Find(&last10Rounds); result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return serviceSummaries, nil
			}
			return nil, result.Error
		}
		for i := range last10Rounds {
			checks := last10Rounds[i].Checks
			for j := range checks {
				checks[j].Debug = ""
				checks[j].Error = ""
			}
		}

		serviceSummaries = append(serviceSummaries, ServiceSummary{
			ServiceName:  name,
			SlaCount:     int(c),
			Last10Rounds: last10Rounds,
		})
	}

	return serviceSummaries, nil
}

func (d *DB) UpdateTeam(teamID uint, identifier string, active bool) error {
	result := d.db.Table("team_schemas").Where("id = ?", teamID).Updates(map[string]any{"identifier": identifier, "active": active})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (d *DB) GetTeamScore(teamID uint) (int, int, int, error) {
	// get service points
	servicePoints := 0
	rows, err := d.db.Raw("SELECT COALESCE(SUM(points), 0) FROM service_check_schemas WHERE team_id = ? and result = 't'", teamID).Rows()
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "error", err)
		}
	}()
	for rows.Next() {
		if err := rows.Scan(&servicePoints); err != nil {
			slog.Error("failed to scan row", "error", err)
			continue
		}
	}

	// get sla violations
	var slas []SLASchema
	if result := d.db.Table("sla_schemas").Where("team_id = ?", teamID).Find(&slas); result.Error != nil {
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
