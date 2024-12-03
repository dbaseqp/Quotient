package db

import (
	"errors"

	"gorm.io/gorm"
)

// ServiceCheckSchema represents the schema for service checks in the database.
type ServiceCheckSchema struct {
	TeamID      uint
	RoundID     uint
	Round       RoundSchema
	ServiceName string
	Points      int
	Result      bool
	Error       string // error
	Debug       string // informational
}

// GetServiceCheckSumByTeam calculates the sum of points for each team where the result is true.
func GetServiceCheckSumByTeam() (map[uint]any, error) {
	result := make(map[uint]any)
	rows, err := db.Model(ServiceCheckSchema{}).Select("team_id, sum(points) as total").Group("team_id").Having("result = ?", true).Rows()

	if err != nil {
		return nil, err
	}

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

/*
GetServiceCheckSumByRound calculates the cumulative sum of points for each team
across all rounds, taking into account penalties from SLAs. It returns a slice
of maps, where each map corresponds to a round and contains team IDs as keys
and their respective cumulative points as values.
*/
func GetServiceCheckSumByRound() ([]map[uint]int, error) {
	var last RoundSchema

	if r := db.Model(RoundSchema{}).Last(&last); r.Error != nil {
		if errors.Is(r.Error, gorm.ErrRecordNotFound) {
			return []map[uint]int{}, nil
		}
		return nil, r.Error
	}

	// creates array with size of num rounds
	result := make([]map[uint]int, last.ID)

	rows, err := db.Raw(`
		SELECT DISTINCT round_id, team_id, 
			   SUM(CASE WHEN result = '1' THEN points ELSE 0 END) 
			   OVER(PARTITION BY team_id ORDER BY round_id) 
		FROM service_check_schemas 
		ORDER BY team_id, round_id
	`).Rows()
	if err != nil {
		return nil, err
	}

	defer rows.Close()
	for rows.Next() {
		var id uint
		var team uint
		var points int

		rows.Scan(&id, &team, &points)

		roundidx := int(id) - 1
		if result[roundidx] == nil {
			result[roundidx] = make(map[uint]int)
		}

		// id starts at 1 so 0 index needs -1
		result[roundidx][team] = points
	}

	rows, err = db.Table("sla_schemas").Select("round_id, team_id, sum(penalty) as penalty").Group("round_id, team_id").Rows()
	if err != nil {
		return nil, err
	}

	defer rows.Close()
	for rows.Next() {
		var id uint
		var team uint
		var penalty int

		rows.Scan(&id, &team, &penalty)

		roundidx := int(id) - 1
		if result[roundidx] == nil {
			result[roundidx] = make(map[uint]int)
		}

		// id starts at 1 so 0 index needs -1
		for i := roundidx; i < len(result); i++ {
			result[i][team] -= penalty
		}
	}

	return result, nil
}

// GetServiceAllChecksByTeam returns all checks for a service, which is one per round
func GetServiceAllChecksByTeam(teamID uint, serviceID string) ([]ServiceCheckSchema, error) {
	var checks []ServiceCheckSchema
	result := db.Table("service_check_schemas").Preload("Round").Where("team_id = ? AND service_name = ?", teamID, serviceID).Order("round_id desc").Find(&checks)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return checks, nil
		}
		return nil, result.Error
	}
	return checks, nil
}

/*
Uptime represents the uptime statistics for a service, including the number
of passed checks and the total number of checks.
*/
type Uptime struct {
	PassedChecks int
	TotalChecks  int
}

/*
LoadUptimes populates the uptime statistics for each service and team.

It calculates the number of passed checks and total checks for each service
and stores the results in the provided `uptimePerService` map, which is
organized by team ID and service name.
*/
func LoadUptimes(uptimePerService *map[uint]map[string]Uptime) error {
	rows, err := db.Raw(`
		SELECT team_id, service_name, 
			   SUM(CASE WHEN result = true THEN 1 ELSE 0 END) as passed_checks, 
			   COUNT(*) as total_checks 
		FROM service_check_schemas 
		GROUP BY team_id, service_name
	`).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var teamID uint
		var serviceName string
		var passedChecks int
		var totalChecks int

		if err := rows.Scan(&teamID, &serviceName, &passedChecks, &totalChecks); err != nil {
			return err
		}

		if (*uptimePerService)[teamID] == nil {
			(*uptimePerService)[teamID] = make(map[string]Uptime)
		}

		(*uptimePerService)[teamID][serviceName] = Uptime{
			PassedChecks: passedChecks,
			TotalChecks:  totalChecks,
		}
	}
	return nil
}

/*
LoadSLAs populates SLA penalties for each service and team.

It calculates the number of consecutive failed checks for each service and team.
If the number of failures reaches the SLA threshold, a penalty is applied.
The results are stored in the provided `slaPerService` map, organized by team ID
and service name.
*/
func LoadSLAs(slaPerService *map[uint]map[string]int, slaThreshold int) error {
	rows, err := db.Table("service_check_schemas").Select("team_id, service_name, result").Rows()
	if err != nil {
		return err
	}
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
