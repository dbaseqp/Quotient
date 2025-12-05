package checks

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand"
	"regexp"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

type Sql struct {
	Service
	Kind  string
	Query []queryData
}

type queryData struct {
	UseRegex bool
	Command  string `toml:",omitempty"`
	Database string `toml:",omitempty"`
	Output   string `toml:",omitempty"`
}

func (c Sql) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Select a random query
		// If no queries defined, just use empty query
		var q queryData
		if len(c.Query) != 0 {
			q = c.Query[rand.Intn(len(c.Query))] // #nosec G404 -- non-crypto selection of query to test
		}

		// Open the DB handle
		db, err := sql.Open(c.Kind, fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", username, password, c.Target, c.Port, q.Database))
		if err != nil {
			checkResult.Error = "creating db handle failed"
			checkResult.Debug = "error: " + err.Error() + ", creds " + username + ":" + password
			response <- checkResult
			return
		}
		defer func() {
		if err := db.Close(); err != nil {
			slog.Error("failed to close sql database", "error", err)
		}
	}()

		// Check DB connection
		err = db.PingContext(context.TODO())
		if err != nil {
			checkResult.Error = "db connection or login failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// If no query command to run, return success
		if len(c.Query) == 0 || q.Command == "" {
			checkResult.Debug = "no query command specified, only checking connection. creds used were " + username + ":" + password
			checkResult.Status = true
			response <- checkResult
			return
		}

		// Query the DB
		var rows *sql.Rows
		rows, err = db.QueryContext(context.TODO(), q.Command)
		if err != nil {
			checkResult.Error = "could not query db with command " + q.Command
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close sql rows", "error", err)
		}
	}()

		// If no output to check, return success
		if q.Output == "" {
			checkResult.Debug = "ran query sucessfully and no output to check against. creds used were " + username + ":" + password
			checkResult.Status = true
			response <- checkResult
			return
		}

		// Check the rows
		re := regexp.MustCompile(q.Output)
		cols, err := rows.Columns()
		if err != nil {
			// handle error
			checkResult.Error = "could not get sql columns"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Make a slice for the values
		row := make([][]byte, len(cols))
		rowPtr := make([]any, len(cols))
		for i := range row {
			rowPtr[i] = &row[i]
		}

		for rows.Next() {
			// Grab a value
			err := rows.Scan(rowPtr...)
			if err != nil {
				checkResult.Error = "could not get row values"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}

			// TODO: by default we check against the first column
			// Check the regex match
			if q.UseRegex {
				if re.Match(row[0]) {
					checkResult.Status = true
					checkResult.Debug = "found regex match: " + string(row[0]) + ". creds used were " + username + ":" + password
					response <- checkResult
					return
				}
				// Check the direct string match
			} else {
				if strings.TrimSpace(string(row[0])) == q.Output {
					checkResult.Status = true
					checkResult.Debug = "found exact string match: " + string(row[0]) + ". creds used were " + username + ":" + password
					response <- checkResult
					return
				}
			}
		}

		// Check for error in the rows
		if rows.Err() != nil {
			checkResult.Error = "sql rows experienced an error"
			checkResult.Debug = rows.Err().Error()
			response <- checkResult
			return
		}

		// No matches found
		checkResult.Debug = "no matching output found for query. creds used were " + username + ":" + password
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Sql) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Sql"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "sql"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Kind == "" {
		c.Kind = "mysql"
	}
	if c.Port == 0 {
		c.Port = 3306
	}
	for _, q := range c.Query {
		if q.UseRegex {
			regexp.MustCompile(q.Output)
		}
	}
	return nil
}
