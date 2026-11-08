package db

import "time"

// TeamOutage is a team whose every check failed in each scored round since it last passed one.
type TeamOutage struct {
	TeamName   string    `json:"team_name"`
	RoundsDown int       `json:"rounds_down"`
	DownSince  time.Time `json:"down_since"`
}

// GetTeamOutages returns the teams whose checks all failed in the latest scored round, with
// how many of their rounds have failed in a row and when the first of those started.
func (d *DB) GetTeamOutages() ([]TeamOutage, error) {
	outages := []TeamOutage{}
	err := d.db.Raw(`
		WITH up AS (
			SELECT team_id,
				COALESCE(MAX(round_id) FILTER (WHERE result), 0) AS last_up,
				MAX(round_id) AS last_seen
			FROM service_check_schemas
			GROUP BY team_id
		)
		SELECT t.name AS team_name,
			COUNT(DISTINCT s.round_id) AS rounds_down,
			MIN(r.start_time) AS down_since
		FROM up
		JOIN service_check_schemas s ON s.team_id = up.team_id AND s.round_id > up.last_up
		JOIN round_schemas r ON r.id = s.round_id
		JOIN team_schemas t ON t.id = up.team_id
		WHERE up.last_seen = (SELECT MAX(id) FROM round_schemas)
		GROUP BY up.team_id, t.name
		ORDER BY up.team_id`).Scan(&outages).Error
	return outages, err
}
