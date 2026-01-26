package db

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"

	"quotient/engine/config"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	db *gorm.DB
)

func Connect(connectURL string) {
	var err error

	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			IgnoreRecordNotFoundError: true, // Ignore ErrRecordNotFound error for logger
		},
	)

	db, err = gorm.Open(postgres.Open(connectURL), &gorm.Config{
		TranslateError: true,
		Logger:         newLogger,
	})
	if err != nil {
		log.Fatalf("Failed to connect database! %s", connectURL)
	}

	slog.Info("Connected to DB")

	err = db.AutoMigrate(&AnnouncementSchema{},
		&TeamSchema{}, &RoundSchema{}, &ServiceCheckSchema{}, &SLASchema{}, &ManualAdjustmentSchema{},
		&InjectSchema{}, &SubmissionSchema{}, &TeamServiceCheckSchema{},
		// box schema must come first for automigrate to work
		&VulnSchema{}, &BoxSchema{}, &VectorSchema{}, &AttackSchema{}, &CompetitionStateSchema{})
	if err != nil {
		log.Fatalln("Failed to auto migrate:", err)
	}

	// Create materialized view for cumulative scores
	err = db.Exec(`
		CREATE MATERIALIZED VIEW IF NOT EXISTS cumulative_scores AS
		SELECT DISTINCT 
			round_id, 
			team_id, 
			SUM(CASE WHEN result = '1' THEN points ELSE 0 END) 
				OVER(PARTITION BY team_id ORDER BY round_id) as cumulative_points
		FROM service_check_schemas 
		ORDER BY team_id, round_id
	`).Error
	if err != nil {
		log.Fatalln("Failed to create materialized view:", err)
	}

	// Create unique index to enable CONCURRENT refresh
	err = db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_cumulative_scores_round_team 
		ON cumulative_scores (round_id, team_id)
	`).Error
	if err != nil {
		log.Fatalln("Failed to create unique index on materialized view:", err)
	}
}

func AddTeams(conf *config.ConfigSettings) error {
	for _, team := range conf.Team {
		t := TeamSchema{Name: team.Name}
		result := db.Where(&t).First(&t)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				if _, err := CreateTeam(t); err != nil {
					return err
				}
			} else {
				return result.Error
			}
		}
	}

	// check for teams from other sources
	// ldap
	if conf.LdapSettings != (config.LdapAuthConfig{}) {
		conn, err := ldap.DialURL(conf.LdapSettings.LdapConnectUrl)
		if err != nil {
			return err
		}
		defer conn.Close()

		err = conn.Bind(conf.LdapSettings.LdapBindDn, conf.LdapSettings.LdapBindPassword)
		if err != nil {
			return err
		}

		searchRequest := ldap.NewSearchRequest(
			conf.LdapSettings.LdapSearchBaseDn,
			ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
			fmt.Sprintf("(&(objectClass=person)(memberOf=%s))", conf.LdapSettings.LdapTeamGroupDn),
			[]string{"sAMAccountName"},
			nil,
		)

		sr, err := conn.Search(searchRequest)
		if err != nil {
			return err
		}

		for _, entry := range sr.Entries {
			teamName := entry.GetAttributeValue("sAMAccountName")
			t := TeamSchema{Name: teamName}
			result := db.Where(&t).First(&t)
			if result.Error != nil {
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					if _, err := CreateTeam(t); err != nil {
						return err
					}
				} else {
					return result.Error
				}
			}
		}
	}
	return nil
}

func ResetScores() error {
	// truncate servicecheckschemas, slaschemas, and roundschemas with cascade
	if err := db.Exec("TRUNCATE TABLE service_check_schemas, round_schemas, sla_schemas CASCADE").Error; err != nil {
		return err
	}

	// Refresh the materialized view to clear it
	if err := db.Exec("REFRESH MATERIALIZED VIEW cumulative_scores").Error; err != nil {
		return err
	}

	return nil
}
