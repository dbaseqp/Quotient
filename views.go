package main

import (
	"html/template"
	"net/http"
	"quotient/checks"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	templateFuncs = template.FuncMap{
		"format": func(t time.Time) string {
			return t.In(loc).Format("2006-01-02 15:04:05 PM")
		},
		"typeOf": func(v interface{}) string {
			return strings.Split(reflect.TypeOf(v).String(), ".")[1]
		},
		"uint": func(n float64) uint {
			return uint(n)
		},
		"keys": func(m any) []string {
			// sorry, i couldn't figure out how to do this...
			var keys []string
			switch temp := m.(type) {
			case map[string]map[uint]map[string]string:
				for key := range temp {
					keys = append(keys, key)
				}
			case map[string]string:
				for key := range temp {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			return keys
		},
	}
)

// default data injected into page
func pageData(c *gin.Context, ginMap gin.H) gin.H {
	data := gin.H{}
	tok, err := c.Cookie("auth_token")
	if err != nil {
		data["error"] = err
	}
	claims, err := getClaimsFromToken(tok)
	if err != nil {
		data["error"] = err
	}

	data["user"] = claims
	data["title"] = ""
	data["now"] = time.Now()
	data["name"] = "QUOTIENT"
	data["config"] = eventConf
	data["error"] = ""
	data["loc"] = loc
	for key, value := range ginMap {
		data[key] = value
	}
	return data
}

func viewIndex(c *gin.Context) {
	isLoggedIn, err := isLoggedIn(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isLoggedIn == false {
		c.Redirect(http.StatusSeeOther, "/login")
	} else {
		c.Redirect(http.StatusSeeOther, "/announcements")
	}
}

func viewAnnouncements(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/announcements.html")
	announcements, err := dbGetAnnouncements()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Announcements", "announcements": announcements}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewScoreboard(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/scoreboard.html")
	err := tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Scoreboard"}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewOverview(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/overview.html")
	teams, err := dbGetTeams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Overview", "teams": teams}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewInjects(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/injects.html")
	injects, err := dbGetInjects()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var team []TeamData
	if claims.Admin {
		teams, err := dbGetTeams()
		if err != nil {
			c.JSON(http.StatusInternalServerError, err.Error())
			return
		}
		team = make([]TeamData, len(teams))
		for i, t := range teams {
			team[i], err = dbGetTeamScore(int(t.ID))
			if err != nil {
				c.JSON(http.StatusInternalServerError, err.Error())
				return
			}
		}
	} else {
		team = make([]TeamData, 1)
		team[0], err = dbGetTeamScore(int(claims.ID))
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Injects", "injects": injects, "team": team}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewInject(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	injectid, _ := strconv.Atoi(c.Param("injectid"))
	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	teams, err := dbGetTeams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Injects", "inject": inject, "teams": teams}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewPCRs(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/pcr.html")
	teams, err := dbGetTeams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "PCRs", "teams": teams, "credentials": credentials}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewLogin(c *gin.Context) {
	isLoggedIn, err := isLoggedIn(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isLoggedIn == true {
		c.Redirect(http.StatusSeeOther, "/")
	}

	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/login.html")
	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Log In"}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}

func viewEngine(c *gin.Context) {
	tmpl, _ := template.Must(template.ParseGlob("templates/layouts/*.html")).ParseGlob("templates/partials/*.html")
	tmpl.Funcs(templateFuncs)
	tmpl.ParseFiles("templates/engine.html")
	teams, err := dbGetTeams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	adjustments, err := dbGetManualAdjustments()
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	var services []checks.Runner
	for _, box := range eventConf.Box {
		services = append(services, box.Runners...)
	}

	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Engine", "status": !enginePause, "teams": teams, "adjustments": adjustments, "services": services, "credentials": credentials}))
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}
