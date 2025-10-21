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
		&VulnSchema{}, &BoxSchema{}, &VectorSchema{}, &AttackSchema{})
	if err != nil {
		log.Fatalln("Failed to auto migrate:", err)
	}
}

func AddTeams(conf *config.ConfigSettings) error {
	// Auto-generate teams if TeamCount is specified
	if conf.MiscSettings.TeamCount > 0 {
		for i := 1; i <= conf.MiscSettings.TeamCount; i++ {
			teamName := fmt.Sprintf("team%02d", i)
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

	// Also add explicitly defined teams
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

	return nil
}
