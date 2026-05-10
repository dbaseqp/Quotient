package checks

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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
		// If no queries defined, just check connection with an empty database string
		if len(c.Query) == 0 {
			// Open the DB handle
			db, err := sql.Open(c.Kind, fmt.Sprintf("%s:%s@tcp(%s:%d)/", username, password, c.Target, c.Port))
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
			checkResult.Debug = "no queries specified, only checking connection. creds used were " + username + ":" + password
			checkResult.Status = true
			response <- checkResult
			return
		}

		checkResult = RunSubchecks(c.Query, c.CheckAll, checkResult, "creds used were "+username+":"+password, func(q queryData, res Result) Result {
			// Open the DB handle
			db, err := sql.Open(c.Kind, fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", username, password, c.Target, c.Port, q.Database))
			if err != nil {
				res.Error = "creating db handle failed"
				res.Debug = "error: " + err.Error()
				res.Status = false
				return res
			}
			defer func() {
			if err := db.Close(); err != nil {
				slog.Error("failed to close sql database", "error", err)
			}
		}()

			// Check DB connection
			err = db.PingContext(context.TODO())
			if err != nil {
				res.Error = "db connection or login failed"
				res.Debug = err.Error()
				res.Status = false
				return res
			}

			// If no query command to run, return success
			if q.Command == "" {
				res.Status = true
				return res
			}

			// Query the DB
			rows, err := db.QueryContext(context.TODO(), q.Command)
			if err != nil {
				res.Error = "could not query db with command " + q.Command
				res.Debug = err.Error()
				res.Status = false
				return res
			}
			defer func() {
			if err := rows.Close(); err != nil {
				slog.Error("failed to close sql rows", "error", err)
			}
		}()

			// If no output to check, return success
			if q.Output == "" {
				res.Debug = "ran query sucessfully and no output to check against"
				res.Status = true
				return res
			}

			// Check the rows
			re := regexp.MustCompile(q.Output)
			cols, err := rows.Columns()
			if err != nil {
				// handle error
				res.Error = "could not get sql columns"
				res.Debug = err.Error()
				res.Status = false
				return res
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
					res.Error = "could not get row values"
					res.Debug = err.Error()
					res.Status = false
					return res
				}

				// TODO: by default we check against the first column
				// Check the regex match
				if q.UseRegex {
					if re.Match(row[0]) {
						res.Status = true
						res.Debug = "found regex match: " + string(row[0])
						return res
					}
				// Check the direct string match
				} else {
					if strings.TrimSpace(string(row[0])) == q.Output {
						res.Status = true
						res.Debug = "found exact string match: " + string(row[0])
						return res
					}
				}
			}

			// Check for error in the rows
			if rows.Err() != nil {
				res.Error = "sql rows experienced an error"
				res.Debug = rows.Err().Error()
				res.Status = false
				return res
			}

			// No matches found
			res.Debug = "no matching output found for query " + q.Command
			res.Status = false
			return res
		})

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
