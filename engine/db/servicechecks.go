package db

import (
	"cmp"
	"errors"
	"math"
	"slices"

	"gorm.io/gorm"
)

type ServiceCheckSchema struct {
	TeamID      uint `gorm:"index:idx_team_round"`
	RoundID     uint `gorm:"index:idx_team_round"`
	Round       RoundSchema
	ServiceName string
	Points      int
	Result      bool
	Error       string // error
	Debug       string // informational
}

func (d *DB) GetServiceCheckSumByTeam() (map[uint]any, error) {
	result := make(map[uint]any)
	rows, err := d.db.Model(ServiceCheckSchema{}).Select("team_id, sum(points) as total").Group("team_id").Having("result = ?", true).Rows()

	if err != nil {
		return nil, err
	}

	// nolint:errcheck
	defer rows.Close()
	for rows.Next() {
		var id uint
		var points int
		err = rows.Scan(&id, &points)
		if err != nil {
			return nil, err
		}

		result[id] = points
	}
	return result, nil
}

// GetServiceCheckSumByRound returns each team's running total (earned points
// minus SLA penalties) after every round, indexed by round ID - 1. Points come
// from the cumulative_scores view, and only penalties of rounds the view covers
// are applied, so every total comes from the same refresh; rounds stored since
// then appear after the next refresh. A team with no checks in a round (lost
// round, all its checks disabled) keeps its previous total.
func (d *DB) GetServiceCheckSumByRound() ([]map[uint]int, error) {
	// Query from materialized view instead of running window function each time
	pointRows, err := d.db.Raw(`
		SELECT round_id, team_id, cumulative_points
		FROM cumulative_scores
		ORDER BY team_id, round_id
	`).Rows()
	if err != nil {
		return nil, err
	}

	// nolint:errcheck
	defer pointRows.Close()
	points := make(map[uint]map[uint]int) // team -> round -> cumulative points
	var lastRound uint
	for pointRows.Next() {
		var id uint
		var team uint
		var cumulative int

		if err := pointRows.Scan(&id, &team, &cumulative); err != nil {
			return nil, err
		}

		// Validate id fits in int and is valid for array indexing
		if id == 0 || id > math.MaxInt {
			continue // Skip invalid round IDs
		}

		if points[team] == nil {
			points[team] = make(map[uint]int)
		}
		points[team][id] = cumulative
		lastRound = max(lastRound, id)
	}
	if err := pointRows.Err(); err != nil {
		return nil, err
	}
	if lastRound == 0 {
		return []map[uint]int{}, nil
	}

	penaltyRows, err := d.db.Table("sla_schemas").Select("round_id, team_id, sum(penalty) as penalty").Where("round_id BETWEEN 1 AND ?", lastRound).Group("round_id, team_id").Rows()
	if err != nil {
		return nil, err
	}

	// nolint:errcheck
	defer penaltyRows.Close()
	penalties := make(map[uint]map[uint]int) // team -> round -> penalty
	for penaltyRows.Next() {
		var id uint
		var team uint
		var penalty int

		if err := penaltyRows.Scan(&id, &team, &penalty); err != nil {
			return nil, err
		}

		if penalties[team] == nil {
			penalties[team] = make(map[uint]int)
		}
		penalties[team][id] = penalty
		if points[team] == nil {
			points[team] = make(map[uint]int)
		}
	}
	if err := penaltyRows.Err(); err != nil {
		return nil, err
	}

	result := make([]map[uint]int, lastRound)
	for i := range result {
		result[i] = make(map[uint]int)
	}
	for team, cumulative := range points {
		total, penalty, seen := 0, 0, false
		for round := uint(1); round <= lastRound; round++ {
			c, scored := cumulative[round]
			p, penalized := penalties[team][round]
			if scored {
				total = c
			}
			penalty += p
			seen = seen || scored || penalized
			if seen {
				// id starts at 1 so 0 index needs -1
				result[round-1][team] = total - penalty
			}
		}
	}

	return result, nil
}

// GetServiceAllChecksByTeam returns all checks for a service, which is one per round
func (d *DB) GetServiceAllChecksByTeam(teamID uint, serviceID string) ([]ServiceCheckSchema, error) {
	var checks []ServiceCheckSchema
	result := d.db.Table("service_check_schemas").Preload("Round").Where("team_id = ? AND service_name = ?", teamID, serviceID).Order("round_id desc").Find(&checks)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return checks, nil
		}
		return nil, result.Error
	}
	return checks, nil
}

type Uptime struct {
	PassedChecks int
	TotalChecks  int
}

func (d *DB) loadUptimes(table string) (map[uint]map[string]Uptime, error) {
	uptimes := make(map[uint]map[string]Uptime)
	rows, err := d.db.Table(table).
		Select("team_id, service_name, SUM(CASE WHEN result = true THEN 1 ELSE 0 END) as passed_checks, COUNT(*) as total_checks").
		Group("team_id, service_name").
		Rows()
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

func (d *DB) LoadUptimes(uptimePerService *map[uint]map[string]Uptime) error {
	uptimes, err := d.loadUptimes("service_check_schemas")
	if err != nil {
		return err
	}
	*uptimePerService = uptimes
	return nil
}

type ServiceScoreData struct {
	TeamID      uint
	ServiceName string
	Points      int
	// MaxPoints is what the team would hold had every one of its checks of the service passed.
	MaxPoints    int
	Violations   int
	TotalPenalty int
}

func (d *DB) GetServiceScores() ([]ServiceScoreData, error) {
	var results []ServiceScoreData

	// Create a map to store intermediate results keyed by team_id and service_name
	scoreMap := make(map[uint]map[string]*ServiceScoreData)

	// First get the earned and the possible points per service per team
	pointsRows, err := d.db.Raw(`
		SELECT team_id, service_name,
			   SUM(CASE WHEN result = true THEN points ELSE 0 END) as total_points,
			   SUM(points) as max_points
		FROM service_check_schemas
		GROUP BY team_id, service_name
	`).Rows()
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer pointsRows.Close()

	for pointsRows.Next() {
		var teamID uint
		var serviceName string
		var points int
		var maxPoints int

		if err := pointsRows.Scan(&teamID, &serviceName, &points, &maxPoints); err != nil {
			return nil, err
		}

		if scoreMap[teamID] == nil {
			scoreMap[teamID] = make(map[string]*ServiceScoreData)
		}

		scoreMap[teamID][serviceName] = &ServiceScoreData{
			TeamID:      teamID,
			ServiceName: serviceName,
			Points:      points,
			MaxPoints:   maxPoints,
		}
	}

	// Then get the total penalties and violation counts per service per team from SLA
	slaRows, err := d.db.Raw(`
		SELECT team_id, service_name, COUNT(*) as violations, SUM(penalty) as total_penalty
		FROM sla_schemas
		GROUP BY team_id, service_name
	`).Rows()
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer slaRows.Close()

	for slaRows.Next() {
		var teamID uint
		var serviceName string
		var violations int
		var totalPenalty int

		if err := slaRows.Scan(&teamID, &serviceName, &violations, &totalPenalty); err != nil {
			return nil, err
		}

		if scoreMap[teamID] == nil {
			scoreMap[teamID] = make(map[string]*ServiceScoreData)
		}

		if scoreMap[teamID][serviceName] == nil {
			scoreMap[teamID][serviceName] = &ServiceScoreData{
				TeamID:      teamID,
				ServiceName: serviceName,
			}
		}

		scoreMap[teamID][serviceName].Violations = violations
		scoreMap[teamID][serviceName].TotalPenalty = totalPenalty
	}

	// Convert map to slice
	for _, services := range scoreMap {
		for _, data := range services {
			results = append(results, *data)
		}
	}

	slices.SortFunc(results, func(a, b ServiceScoreData) int {
		if a.TeamID != b.TeamID {
			return cmp.Compare(a.TeamID, b.TeamID)
		}
		return cmp.Compare(a.ServiceName, b.ServiceName)
	})
	return results, nil
}

func (d *DB) LoadSLAs(slaPerService *map[uint]map[string]int, slaThreshold int) error {
	// Streaks are replayed in round order; without ORDER BY Postgres may
	// return rows in any order (e.g. a synchronized seq scan starts mid-table).
	rows, err := d.db.Table("service_check_schemas").Select("team_id, service_name, result").Order("round_id").Rows()
	if err != nil {
		return err
	}
	// nolint:errcheck
	defer rows.Close()

	for rows.Next() {
		var teamID uint
		var serviceName string
		var result bool

		if err := rows.Scan(&teamID, &serviceName, &result); err != nil {
			return err
		}

		if (*slaPerService)[teamID] == nil {
			(*slaPerService)[teamID] = make(map[string]int)
		}

		if result {
			(*slaPerService)[teamID][serviceName] = 0
		} else {
			(*slaPerService)[teamID][serviceName] = ((*slaPerService)[teamID][serviceName] + 1) % slaThreshold
		}
	}
	return nil
}

// CheckResult is one team's result for one service in one round.
type CheckResult struct {
	RoundID     uint
	TeamID      uint
	ServiceName string
	Result      bool
}

// GetCheckResults returns the check results of the given teams in round
// order, without their error and debug text.
func (d *DB) GetCheckResults(teamIDs []uint) ([]CheckResult, error) {
	var results []CheckResult
	err := d.db.Table("service_check_schemas").
		Select("round_id, team_id, service_name, result").
		Where("team_id IN ?", teamIDs).
		Order("round_id").
		Find(&results).Error
	return results, err
}
