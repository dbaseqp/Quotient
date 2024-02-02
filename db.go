package main

import (
	"log"
	"sugmaase/checks"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	//"log"
)

const (
	EMPTY = iota
	SUBMITTED
	GRADED
)

var (
	DatasStaging = []TeamData{}
)

// schema structs

type TeamData struct {
	ID                     uint
	Name                   string `gorm:"unique"` // https://www.postgresql.org/docs/current/functions-sequence.html#:~:text=Caution,of%20assigned%20values
	Pw                     string
	IP                     int `gorm:"unique"`
	Token                  string
	CumulativeServiceScore int
	DeletedAt              time.Time
	Checks                 []ServiceData          `gorm:"foreignKey:TeamID"` // get checks who belong to this team
	ManualAdjustments      []ManualAdjustmentData `gorm:"foreignKey:TeamID"` // get adjustments who belong to this team
	SLAs                   []SLAData              `gorm:"foreignKey:TeamID"` // get slas who belong to this team
	SubmissionData         []SubmissionData       `gorm:"foreignKey:TeamID"` // get inject submissions who belong to this team
}

type RoundData struct {
	ID        uint
	StartTime time.Time
	Checks    []ServiceData `gorm:"foreignKey:RoundID"` // get checks run this round
	SLAs      []SLAData     `gorm:"foreignKey:RoundID"`
}

// summary table just cuz it makes data so much eaiser
type RoundPointsData struct {
	TeamID          uint
	RoundID         uint
	PointsThisRound int
}

type ServiceData struct {
	TeamID      uint
	RoundID     uint
	Round       RoundData
	ServiceName string
	Points      int
	Result      bool
	Error       string // error
	Debug       string // informational
}

type SLAData struct {
	TeamID      uint
	RoundID     uint
	Round       RoundData
	ServiceName string
	Penalty     int
}

type ManualAdjustmentData struct {
	TeamID    uint
	Team      TeamData
	CreatedAt time.Time
	Amount    int
	Reason    string
}

// consider adding max score field but maybe not necessary cuz just set submission score to real point value based on rubric
type InjectData struct {
	ID              uint
	Title           string `gorm:"unique"` // also used as directory name
	Description     string
	OpenTime        time.Time
	DueTime         time.Time
	CloseTime       time.Time
	InjectFileNames pq.StringArray `gorm:"type:text[]"`
}

type SubmissionData struct {
	TeamID              uint
	InjectID            uint
	SubmissionTime      time.Time
	DeletedAt           time.Time
	Score               int
	Feedback            string
	Grader              string
	SubmissionFileNames pq.StringArray `gorm:"type:text[]"`
	AttemptNumber       int
}

type AnnouncementData struct {
	ID        uint
	CreatedAt time.Time
	Content   string
}

// database methods
func dbLogin(username string, password string) (uint, error) {
	var team TeamData

	result := db.Where("name = ? AND pw = ?", username, password).First(&team)

	if result.Error != nil {
		return 0, result.Error
	}

	return team.ID, nil
}

func dbGetChecks() (map[uint][]RoundPointsData, error) {
	records := make(map[uint][]RoundPointsData)
	teams, err := dbGetTeams()
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		var results []RoundPointsData
		result := db.Where("team_id = ?", team.ID).Order("round_id").Find(&results)
		if result.Error != nil {
			return nil, result.Error
		}
		records[team.ID] = results
	}
	return records, nil
}

func dbGetChecksThisRound(roundid int) (map[uint][]ServiceData, error) {
	records := make(map[uint][]ServiceData)
	teams, err := dbGetTeams()
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		var results []ServiceData
		result := db.Where("team_id = ? AND round_id = ?", team.ID, roundid).Order("service_name").Find(&results)
		if result.Error != nil {
			return nil, result.Error
		}
		records[team.ID] = results
	}
	return records, nil
}

func dbCreateSLA(teamid uint, servicename string, roundNumber int, penalty int) error {
	result := db.Create(&SLAData{TeamID: teamid, ServiceName: servicename, RoundID: uint(roundNumber), Penalty: penalty})

	if result.Error != nil {
		return result.Error
	}
	return nil
}

// accurately recalculate scores in case caches get off
func dbCalculateCumulativeServiceScore() error {
	var sums []struct {
		TeamID      uint
		TotalPoints int
	}
	result := db.Raw("SELECT team_id, SUM(points_this_round) AS total_points FROM round_points_data GROUP BY team_id").Scan(&sums)

	if result.Error != nil {
		return result.Error
	}

	for _, sum := range sums {
		result := db.Model(&TeamData{ID: sum.TeamID}).Update("cumulative_service_score", sum.TotalPoints)
		if result.Error != nil {
			log.Println()
		}
	}
	return nil
}

// Update cumulative_points in teams table in memory efficient way by only updating relative scores
func dbUpdateCumulativeServiceScoreCache(roundData map[uint][]checks.Result) error {
	teams, err := dbGetTeams()
	if err != nil {
		return err
	}

	for _, team := range teams {
		sum := team.CumulativeServiceScore
		for _, check := range roundData[team.ID] {
			sum += check.Points
		}
		result := db.Model(&team).Update("cumulative_service_score", sum)
		if result.Error != nil {
			errorPrint(result.Error)
			// i dont want to cancel the cache update on one team's error
			// return result.Error
		}
	}

	return nil
}

func dbGetManualAdjustments() ([]ManualAdjustmentData, error) {
	var adjustments []ManualAdjustmentData

	result := db.Preload(clause.Associations).Order("created_at desc").Find(&adjustments)

	if result.Error != nil {
		return nil, result.Error
	}
	return adjustments, nil
}

func dbResetScoring() error {
	tx := db.Begin()

	result := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ServiceData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SLAData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RoundData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RoundPointsData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ManualAdjustmentData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubmissionData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&AnnouncementData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Model(&TeamData{}).Where("1=1").Update("cumulative_service_score", 0)
	if result.Error != nil {
		return result.Error
	}
	tx.Commit()
	return nil
}

func dbGetLastRoundNumber() (int, error) {
	var count int64
	result := db.Table("round_data").Count(&count)

	if result.Error != nil {
		return 0, result.Error
	}
	return int(count), nil
}

func dbCreateRound(roundNumber int, startTime time.Time) error {
	result := db.Create(&RoundData{ID: uint(roundNumber), StartTime: startTime})

	if result.Error != nil {
		return result.Error
	}
	return nil
}

// given a map of teamid to result data, give points to teams
// this should be fundamentally sound regardless of event type
// need to make sure that holes are acceptable for koth
func dbProcessRound(roundData map[uint][]checks.Result) error {
	tx := db.Begin()
	for teamid := range roundData {
		debugPrint("[SCORE] ===== Saving scores for", teamid)
		var sum int
		for _, res := range roundData[teamid] {
			result := tx.Create(&ServiceData{
				TeamID:      teamid,
				RoundID:     uint(roundNumber),
				ServiceName: res.Name,
				Result:      res.Status,
				Points:      res.Points,
				Error:       res.Error,
				Debug:       res.Debug,
			})

			if result.Error != nil {
				// if there is an error for saving any check, this throws away the entire round so uhhh
				return result.Error
			}
			sum += res.Points
		}
		result := tx.Create(&RoundPointsData{
			TeamID:          teamid,
			RoundID:         uint(roundNumber),
			PointsThisRound: sum,
		})
		if result.Error != nil {
			// if there is an error for saving any check, this throws away the entire round so uhhh pt2
			return result.Error
		}
	}
	tx.Commit()
	return nil
}

func dbGetInjects() ([]InjectData, error) {
	var injects []InjectData

	result := db.Order("open_time desc").Find(&injects)

	if result.Error != nil {
		return nil, result.Error
	}
	return injects, nil
}

func dbGetTeams() ([]TeamData, error) {
	var teams []TeamData

	result := db.Order("id").Find(&teams)

	if result.Error != nil {
		return nil, result.Error
	}
	return teams, nil
}

func dbGetAnnouncements() ([]AnnouncementData, error) {
	var announcements []AnnouncementData

	result := db.Order("created_at desc").Find(&announcements)

	if result.Error != nil {
		return nil, result.Error
	}
	return announcements, nil
}

func dbAddTeam(team TeamData) (uint, error) {
	result := db.Create(&team)

	if result.Error != nil {
		return 0, result.Error
	}
	return team.ID, nil
}

func dbUpdateTeam(team TeamData) error {
	result := db.Updates(&team)

	if result.Error != nil {
		return result.Error
	}

	return nil
}

func dbDeleteTeam(teamid int) error {
	var team TeamData

	tx := db.Begin()
	result := tx.Table("service_data").Where("team_id = ?", teamid).Delete(&ServiceData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Table("submission_data").Where("team_id = ?", teamid).Delete(&SubmissionData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Table("sla_data").Where("team_id = ?", teamid).Delete(&SLAData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Table("manual_adjustment_data").Where("team_id = ?", teamid).Delete(&ManualAdjustmentData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Table("round_points_data").Where("team_id = ?", teamid).Delete(&RoundPointsData{})
	if result.Error != nil {
		return result.Error
	}

	result = tx.Delete(&team, uint(teamid))

	if result.Error != nil {
		return result.Error
	}
	tx.Commit()
	return nil
}

func dbAddInject(inject InjectData) (uint, error) {
	result := db.Create(&inject)

	if result.Error != nil {
		return 0, result.Error
	}
	return inject.ID, nil
}

func dbAddAnnouncement(announcement AnnouncementData) (uint, error) {
	result := db.Create(&announcement)

	if result.Error != nil {
		return 0, result.Error
	}
	return announcement.ID, nil
}

func dbGetTeamScore(teamid int) (TeamData, error) {
	var teamScore TeamData

	result := db.Preload("ManualAdjustments").Preload("SLAs", func(db *gorm.DB) *gorm.DB {
		return db.Order("sla_data.round_id asc")
	}).Preload("SubmissionData").First(&teamScore, uint(teamid))

	if result.Error != nil {
		return TeamData{}, result.Error
	}

	// no idea how performant this will be... maybe gorm was a bad decision
	// this should represent holes and remove services that no longer exist
	var checks []ServiceData

	// only want the last 10 rounds
	subquery := db.Table("round_data").Order("start_time desc").Limit(10)
	// subquery := db.Raw("SELECT * FROM (select * from round_data order by start_time desc limit 10) order by start_time asc")
	result = db.Preload("Round").Where("team_id = ?", teamid).Joins("INNER JOIN (?) as rounds on service_data.round_id = rounds.id", subquery).Find(&checks)

	if result.Error != nil {
		return TeamData{}, result.Error
	}
	// checks are not grouped by their service name
	teamScore.Checks = checks

	return teamScore, nil
}

func dbGetInject(injectid int) (InjectData, error) {
	var inject InjectData

	result := db.First(&inject, uint(injectid))

	if result.Error != nil {
		return InjectData{}, result.Error
	}

	return inject, nil
}

func dbUpdateInject(inject InjectData) error {
	result := db.Save(&inject)

	if result.Error != nil {
		return result.Error
	}

	return nil
}

func dbDeleteInject(injectid int) error {
	result := db.Delete(&InjectData{}, uint(injectid))

	if result.Error != nil {
		return result.Error
	}

	return nil
}

func dbDeleteAnnouncement(announcementid int) error {
	result := db.Delete(&AnnouncementData{}, uint(announcementid))

	if result.Error != nil {
		return result.Error
	}

	return nil
}

func dbSubmitInject(submission SubmissionData) error {
	result := db.Create(&submission)

	if result.Error != nil {
		return result.Error
	}
	return nil
}

// if no submissions, this effectively does nothing but still returns success
func dbGradeInjectSubmission(submission SubmissionData) error {
	result := db.Table("submission_data").Where(&SubmissionData{TeamID: submission.TeamID, InjectID: submission.InjectID, AttemptNumber: submission.AttemptNumber}).Updates(submission)

	if result.Error != nil {
		return result.Error
	}
	return nil
}

func dbSubmitManualAdjustment(adjustment ManualAdjustmentData) error {
	result := db.Create(&adjustment)

	if result.Error != nil {
		return result.Error
	}
	return nil
}

func dbLoadSubmissions() (map[int]map[int]int, error) {
	type submissionCounts struct {
		TeamID   uint
		InjectID uint
		Count    int
	}

	injects, err := dbGetInjects()
	if err != nil {
		return nil, err
	}

	teams, err := dbGetTeams()
	if err != nil {
		return nil, err
	}

	submissions := make(map[int]map[int]int)
	for _, inject := range injects {
		submissions[int(inject.ID)] = make(map[int]int)
		for _, team := range teams {
			submissions[int(inject.ID)][int(team.ID)] = 0 // this will panic if submissions in db exists for non-existent inject
		}
	}
	// is using .Count more efficient? this seems most straight forward...
	rows, err := db.Table("submission_data").Select("team_id, inject_id, count(*) as count").Group("team_id, inject_id").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var submission submissionCounts
		if err := db.ScanRows(rows, &submission); err != nil {
			return nil, err
		}
		submissions[int(submission.InjectID)][int(submission.TeamID)] = submission.Count
	}

	return submissions, nil
}

func dbGetInjectSubmissions(injectid int, teamid int) ([]SubmissionData, error) {
	var submissions []SubmissionData

	result := db.Where("inject_id = ? AND team_id = ?", injectid, teamid).Order("submission_time desc").Find(&submissions)

	if result.Error != nil {
		return nil, result.Error
	}

	return submissions, nil
}

func dbGetTeamServices(teamid int, limit int, servicename string) ([]ServiceData, error) {
	var serviceResults []ServiceData

	subquery := db.Table("round_data").Order("start_time desc").Limit(limit)
	query := db.Preload("Round").Joins("INNER JOIN (?) as rounds on service_data.round_id = rounds.id", subquery).Order("rounds.id desc")
	if servicename != "" {
		query = query.Where("service_name = ?", servicename)
	}
	result := query.Find(&serviceResults, uint(teamid))

	if result.Error != nil {
		return nil, result.Error
	}

	return serviceResults, nil
}
