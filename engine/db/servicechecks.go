package db

import (
	"errors"

	"gorm.io/gorm"
)

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

		if err := rows.Scan(&id, &team, &points); err != nil {
			return nil, err
		}

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

		if err := rows.Scan(&id, &team, &penalty); err != nil {
			return nil, err
		}

		roundidx := int(id) - 1
		if result[roundidx] == nil {
			result[roundidx] = make(map[uint]int)
		}

		// id starts at 1 so 0 index needs -1
		for i := roundidx; i < len(result); i++ {
			if result[i] == nil {
				result[i] = make(map[uint]int)
			}
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

type Uptime struct {
	PassedChecks int
	TotalChecks  int
}

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

type ServiceScoreData struct {
	TeamID       uint
	ServiceName  string
	Points       int
	Violations   int
	TotalPenalty int
}

func GetServiceScores() ([]ServiceScoreData, error) {
	var results []ServiceScoreData

	err := db.Model(&ServiceCheckSchema{}).
		Select(`
			service_check_schemas.team_id,
			service_check_schemas.service_name,
			SUM(CASE WHEN service_check_schemas.result = ? THEN service_check_schemas.points ELSE 0 END) as points,
			COUNT(sla_schemas.round_id) as violations,
			COALESCE(SUM(sla_schemas.penalty), 0) as total_penalty
		`, true).
		Joins("LEFT JOIN sla_schemas ON service_check_schemas.team_id = sla_schemas.team_id AND service_check_schemas.service_name = sla_schemas.service_name").
		Group("service_check_schemas.team_id, service_check_schemas.service_name").
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	return results, nil
}

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
