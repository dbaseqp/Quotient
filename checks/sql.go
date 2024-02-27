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
	UseRegex       bool
	Contains       bool
	DatabaseExists bool
	Database       string
	Table          string
	Column         string
	Output         string
}

func (c Sql) Run(teamID uint, boxIp string, res chan Result, service Service) {
	username, password := getCreds(teamID, service.CredLists)

	// Run query
	q := c.Query[rand.Intn(len(c.Query))]

	db, err := sql.Open(c.Kind, fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", username, password, boxIp, service.Port, q.Database))
	if err != nil {
		res <- Result{
			Error: "creating db handle failed",
			Debug: "error: " + err.Error() + ", creds " + username + ":" + password,
		}
		return
	}
	defer db.Close()

	// Check db connection
	err = db.PingContext(context.TODO())
	if err != nil {
		res <- Result{
			Error: "db connection or login failed",
			Debug: err.Error(),
		}
		return
	}

	// Query the DB
	// TODO: This is SQL injectable. Figure out Paramerterized queries. not that it really matters...
	var rows *sql.Rows
	if q.DatabaseExists {
		rows, err = db.QueryContext(context.TODO(), "SHOW DATABASES;")
		if err != nil {
			res <- Result{
				Error: "could not query db for database " + q.Database,
				Debug: err.Error(),
			}
			return
		}
		defer rows.Close()

		q.Contains = true
		q.Output = q.Database
	} else {
		rows, err = db.QueryContext(context.TODO(), fmt.Sprintf("SELECT %s FROM %s;", q.Column, q.Table))
		if err != nil {
			res <- Result{
				Error: "could not query db for database " + q.Database + " table " + q.Table + " column " + q.Column,
				Debug: err.Error(),
			}
			return
		}
		defer rows.Close()
	}
	var output string
	if q.Output != "" {
		foundSwitch := false
		// Check the rows
		for rows.Next() {
			// Grab a value
			err := rows.Scan(&output)
			if err != nil {
				res <- Result{
					Error: "could not get row values",
					Debug: err.Error(),
				}
				return
			}
			if q.Contains {
				if q.UseRegex {
					re := regexp.MustCompile(q.Output)
					found := re.Find([]byte(output))
					if len(found) != 0 {
						foundSwitch = true
						break
					}
				} else {
					if strings.Contains(output, q.Output) {
						foundSwitch = true
						break
					}
				}
			} else {
				if q.UseRegex {
					re := regexp.MustCompile(q.Output)
					if re.Match([]byte(output)) {
						foundSwitch = true
						break
					}
				} else {
					if strings.TrimSpace(output) == q.Output {
						foundSwitch = true
						break
					}
				}
			}
		}
		if !foundSwitch {
			var debug string
			if q.DatabaseExists {
				debug = "database server didn't contain database " + q.Output
			} else {
				debug = "database " + q.Database + " table " + q.Table + " column " + q.Column + " didn't contain " + q.Output
			}
			res <- Result{
				Error: "query output didn't contain value",
				Debug: debug}
			return
		}
		// Check for error in the rows
		if rows.Err() != nil {
			res <- Result{
				Error: "sql rows experienced an error",
				Debug: err.Error(),
			}
			return
		}
	}

	res <- Result{
		Status: true,
		Debug:  "creds used were " + username + ":" + password,
	}
}
