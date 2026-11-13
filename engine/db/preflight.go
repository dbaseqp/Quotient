package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Preflight rounds are kept apart from round_schemas so they never count toward points, uptime or SLAs.
type PreflightRoundSchema struct {
	ID        uint
	StartTime time.Time
	Checks    []PreflightCheckSchema `gorm:"foreignKey:RoundID"`
}

type PreflightCheckSchema struct {
	ID          uint `gorm:"primarykey"`
	RoundID     uint `gorm:"index"`
	TeamID      uint
	Team        TeamSchema
	ServiceName string
	ServiceType string
	Result      bool
	Error       string
	Debug       string
}

func (d *DB) CreatePreflightRound(round PreflightRoundSchema) error {
	return d.db.Create(&round).Error
}

// GetLastPreflightRoundID is 0 when no preflight round has been stored.
func (d *DB) GetLastPreflightRoundID() (uint, error) {
	var last uint
	err := d.db.Model(&PreflightRoundSchema{}).Select("COALESCE(MAX(id), 0)").Scan(&last).Error
	return last, err
}

// GetLatestPreflightRound returns the zero round when none has been stored.
func (d *DB) GetLatestPreflightRound() (PreflightRoundSchema, error) {
	var round PreflightRoundSchema
	err := d.db.
		Preload("Checks", func(tx *gorm.DB) *gorm.DB { return tx.Order("team_id, service_name") }).
		Preload("Checks.Team").
		Order("id desc").First(&round).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PreflightRoundSchema{}, nil
	}
	return round, err
}

// GetLastPreflightRound returns the latest preflight round shaped as a
// RoundSchema for callers that render round status without scoring it.
func (d *DB) GetLastPreflightRound() (RoundSchema, error) {
	pf, err := d.GetLatestPreflightRound()
	if err != nil || pf.ID == 0 {
		return RoundSchema{}, err
	}
	checks := make([]ServiceCheckSchema, 0, len(pf.Checks))
	for _, c := range pf.Checks {
		checks = append(checks, ServiceCheckSchema{
			TeamID:      c.TeamID,
			RoundID:     c.RoundID,
			ServiceName: c.ServiceName,
			Result:      c.Result,
			Error:       c.Error,
			Debug:       c.Debug,
		})
	}
	return RoundSchema{
		ID:        pf.ID,
		StartTime: pf.StartTime,
		Checks:    checks,
	}, nil
}

func (d *DB) GetPreflightUptimes() (map[uint]map[string]Uptime, error) {
	uptimes := make(map[uint]map[string]Uptime)
	rows, err := d.db.Raw(`
		SELECT team_id, service_name,
		       SUM(CASE WHEN result = true THEN 1 ELSE 0 END) as passed_checks,
		       COUNT(*) as total_checks
		FROM preflight_check_schemas
		GROUP BY team_id, service_name
	`).Rows()
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer rows.Close()

	for rows.Next() {
		var teamID uint
		var serviceName string
		var passedChecks int
		var totalChecks int

		if err := rows.Scan(&teamID, &serviceName, &passedChecks, &totalChecks); err != nil {
			return nil, err
		}
		if uptimes[teamID] == nil {
			uptimes[teamID] = make(map[string]Uptime)
		}
		uptimes[teamID][serviceName] = Uptime{
			PassedChecks: passedChecks,
			TotalChecks:  totalChecks,
		}
	}
	return uptimes, rows.Err()
}

func (d *DB) GetPreflightTeamSummary(teamID uint) ([]map[string]any, error) {
	serviceSummaries := []map[string]any{}
	var namePerService []string

	if err := d.db.Model(&PreflightCheckSchema{}).
		Distinct("service_name").
		Where("team_id = ?", teamID).
		Order("service_name").
		Pluck("service_name", &namePerService).Error; err != nil {
		return nil, err
	}
	if len(namePerService) == 0 {
		return serviceSummaries, nil
	}

	var pfRounds []PreflightRoundSchema
	if err := d.db.Preload("Checks", "team_id = ?", teamID).
		Order("id desc").
		Limit(10).
		Find(&pfRounds).Error; err != nil {
		return nil, err
	}

	for _, name := range namePerService {
		last10Rounds := make([]RoundSchema, len(pfRounds))
		for i, pf := range pfRounds {
			var checks []ServiceCheckSchema
			for _, c := range pf.Checks {
				if c.ServiceName == name {
					checks = append(checks, ServiceCheckSchema{
						TeamID:      c.TeamID,
						RoundID:     c.RoundID,
						ServiceName: c.ServiceName,
						Result:      c.Result,
					})
				}
			}
			last10Rounds[i] = RoundSchema{
				ID:        pf.ID,
				StartTime: pf.StartTime,
				Checks:    checks,
			}
		}
		serviceSummaries = append(serviceSummaries, map[string]any{
			"ServiceName":  name,
			"SlaCount":     0,
			"Last10Rounds": last10Rounds,
		})
	}
	return serviceSummaries, nil
}

func (d *DB) GetPreflightServiceAllChecksByTeam(teamID uint, serviceID string) ([]ServiceCheckSchema, error) {
	var pfRounds []PreflightRoundSchema
	if err := d.db.Preload("Checks", "team_id = ? AND service_name = ?", teamID, serviceID).
		Order("id desc").
		Find(&pfRounds).Error; err != nil {
		return nil, err
	}
	checks := make([]ServiceCheckSchema, 0, len(pfRounds))
	for _, pf := range pfRounds {
		for _, r := range pf.Checks {
			checks = append(checks, ServiceCheckSchema{
				TeamID:      r.TeamID,
				RoundID:     r.RoundID,
				Round:       RoundSchema{ID: pf.ID, StartTime: pf.StartTime},
				ServiceName: r.ServiceName,
				Result:      r.Result,
				Error:       r.Error,
				Debug:       r.Debug,
			})
		}
	}
	return checks, nil
}
