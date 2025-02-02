package checks

import (
	"context"
	"database/sql"
	"fmt"
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

		// Run query
		q := c.Query[rand.Intn(len(c.Query))]

		db, err := sql.Open(c.Kind, fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", username, password, c.Target, c.Port, q.Database))
		if err != nil {
			checkResult.Error = "creating db handle failed"
			checkResult.Debug = "error: " + err.Error() + ", creds " + username + ":" + password
			response <- checkResult
			return
		}
		defer db.Close()

		// Check db connection
		err = db.PingContext(context.TODO())
		if err != nil {
			checkResult.Error = "db connection or login failed"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Query the DB
		var rows *sql.Rows
		if q.Command == "" {
			checkResult.Debug = "no query command, only checking connection. creds used were " + username + ":" + password
			checkResult.Status = true
			response <- checkResult
		}

		rows, err = db.QueryContext(context.TODO(), q.Command)
		if err != nil {
			checkResult.Error = "could not query db for database " + q.Command
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer rows.Close()

		var output string
		if q.Output != "" {
			// Check the rows
			for rows.Next() {
				// Grab a value
				err := rows.Scan(&output)
				if err != nil {
					checkResult.Error = "could not get row values"
					checkResult.Debug = err.Error()
					response <- checkResult
					return
				}
				if q.UseRegex {
					re := regexp.MustCompile(q.Output)
					if re.Match([]byte(output)) {
						checkResult.Status = true
						checkResult.Debug = "found regex match: " + output + ". creds used were " + username + ":" + password
						response <- checkResult
					}
				} else {
					if strings.TrimSpace(output) == q.Output {
						checkResult.Status = true
						checkResult.Debug = "found exact string match: " + output + ".creds used were " + username + ":" + password
						response <- checkResult
						break
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
		}

		checkResult.Debug = "desired output " + output + "not found in any output. creds used were " + username + ":" + password
		checkResult.Error = "output incorrect"
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
