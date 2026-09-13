package db

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/dbaseqp/Quotient/engine/config"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	db *gorm.DB
}

func Connect(connectURL string) *DB {
	var err error

	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			IgnoreRecordNotFoundError: true, // Ignore ErrRecordNotFound error for logger
		},
	)

	gormDB, err := gorm.Open(postgres.Open(connectURL), &gorm.Config{
		TranslateError: true,
		Logger:         newLogger,
	})
	if err != nil {
		log.Fatalf("Failed to connect database! %s", connectURL)
	}

	slog.Info("Connected to DB")

	db := &DB{db: gormDB}

	db.migrate()

	return db
}

// migrationLockID identifies the advisory lock that serializes schema
// migration. Any value works as long as every process agrees on it.
const migrationLockID int64 = 0x71756F74

// migrate applies the schema under an advisory lock.
//
// AutoMigrate and CREATE ... IF NOT EXISTS both read the catalog and then
// write, so two processes connecting at once can each decide a table is
// missing and issue CREATE TABLE. The loser gets "relation already exists" or
// a unique violation on pg_type. The lock makes the read-then-write pair
// exclusive; the second process runs its migration afterwards and finds
// nothing to do.
func (d *DB) migrate() {
	sqlDB, err := d.db.DB()
	if err != nil {
		log.Fatalln("Failed to access database handle:", err)
	}

	// The lock is session scoped, so it has to be held on one pinned
	// connection rather than borrowed from the pool per statement.
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		log.Fatalln("Failed to acquire connection for migration lock:", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			slog.Error("failed to release migration lock", "error", err)
		}
		if err := conn.Close(); err != nil {
			slog.Error("failed to close migration lock connection", "error", err)
		}
	}()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		log.Fatalln("Failed to acquire migration lock:", err)
	}

	err = d.db.AutoMigrate(&AnnouncementSchema{},
		&TeamSchema{}, &RoundSchema{}, &ServiceCheckSchema{}, &SLASchema{}, &ManualAdjustmentSchema{},
		&InjectSchema{}, &SubmissionSchema{}, &TeamServiceCheckSchema{},
		// box schema must come first for automigrate to work
		&VulnSchema{}, &BoxSchema{}, &VectorSchema{}, &AttackSchema{}, &CompetitionStateSchema{},
		// credential schemas for PCR management
		&OriginalCredentialSchema{}, &CredentialSchema{}, &PCRHistorySchema{})
	if err != nil {
		log.Fatalln("Failed to auto migrate:", err)
	}

	// Create materialized views
	d.createCumulativeScoresView()
}

func (d *DB) Close() error {
	rawDB, err := d.db.DB()
	if err != nil {
		return err
	}

	return rawDB.Close()
}

// createCumulativeScoresView creates the materialized view for cumulative scores.
func (d *DB) createCumulativeScoresView() {
	// Create the materialized view if it doesn't exist
	// If it does exist, CREATE won't refresh it, so we do that separately
	err := d.db.Exec(`
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
		log.Fatalln("Failed to create cumulative_scores materialized view:", err)
	}

	// Unique index required to enable REFRESH CONCURRENTLY
	err = d.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_cumulative_scores_round_team 
		ON cumulative_scores (round_id, team_id)
	`).Error
	if err != nil {
		log.Fatalln("Failed to create index on cumulative_scores:", err)
	}

	// Ensure view is populated/fresh on startup in case there was existing data
	err = d.db.Exec("REFRESH MATERIALIZED VIEW cumulative_scores").Error
	if err != nil {
		log.Fatalln("Failed to refresh cumulative_scores materialized view:", err)
	}
}

func (d *DB) AddTeams(conf *config.ConfigSettings) error {
	for _, team := range conf.Team {
		t := TeamSchema{Name: team.Name}
		result := d.db.Where(&t).First(&t)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				if _, err := d.CreateTeam(t); err != nil {
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
		// nolint:errcheck
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
			result := d.db.Where(&t).First(&t)
			if result.Error != nil {
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					if _, err := d.CreateTeam(t); err != nil {
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

func (d *DB) ResetScores() error {
	// truncate servicecheckschemas, slaschemas, and roundschemas with cascade
	if err := d.db.Exec("TRUNCATE TABLE service_check_schemas, round_schemas, sla_schemas CASCADE").Error; err != nil {
		return err
	}

	// Refresh the materialized view to clear it
	if err := d.db.Exec("REFRESH MATERIALIZED VIEW cumulative_scores").Error; err != nil {
		return err
	}

	return nil
}
