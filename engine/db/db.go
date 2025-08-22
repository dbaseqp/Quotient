package db

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"

	"quotient/engine/config"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	db *gorm.DB
)

func dialector(connectURL string) gorm.Dialector {
	if strings.HasPrefix(connectURL, "sqlite:") {
		split := strings.SplitN(connectURL, ":", 2)
		filename := split[1]
		return sqlite.Open(fmt.Sprintf("%s?mode=rwc", filename))
	} else {
		return postgres.Open(connectURL)
	}
}

func Connect(connectURL string) {
	var err error

	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			IgnoreRecordNotFoundError: true, // Ignore ErrRecordNotFound error for logger
		},
	)

	db, err = gorm.Open(dialector(connectURL), &gorm.Config{
		TranslateError: true,
		Logger:         newLogger,
	})
	if err != nil {
		log.Fatalf("Failed to connect database %s: %w", connectURL, err)
	}

	slog.Info("Connected to DB")

	err = db.AutoMigrate(&AnnouncementSchema{}, &AnnouncementFileSchema{},
		&TeamSchema{}, &RoundSchema{}, &ServiceCheckSchema{}, &SLASchema{}, &ManualAdjustmentSchema{},
		&InjectSchema{}, &InjectFileSchema{}, &SubmissionSchema{}, &TeamServiceCheckSchema{},
		// box schema must come first for automigrate to work
		&VulnSchema{}, &BoxSchema{}, &VectorSchema{}, &AttackSchema{}, &AttackImageSchema{})
	if err != nil {
		log.Fatalln("Failed to auto migrate:", err)
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
	if db.Dialector.Name() == "postgres" {
		if err := db.Exec("TRUNCATE TABLE service_check_schemas, round_schemas, sla_schemas CASCADE").Error; err != nil {
			return err
		}
	} else {
		return db.Transaction(func(tx *gorm.DB) error {
			// https://gorm.io/docs/delete.html#Block-Global-Delete
			if err := tx.Where("1 = 1").Delete(&ServiceCheckSchema{}).Error; err != nil {
				return err
			}

			if err := tx.Where("1 = 1").Delete(&RoundSchema{}).Error; err != nil {
				return err
			}

			if err := tx.Where("1 = 1").Delete(&SLASchema{}).Error; err != nil {
				return err
			}

			return nil
		})
	}

	return nil
}
