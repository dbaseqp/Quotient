package main

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
)

var (
	templateFuncs = template.FuncMap{
		"format": func(t time.Time) string {
			return t.In(loc).Format("2006-01-02 15:04:05 PM")
		},
		"uint": func(n float64) uint {
			return uint(n)
		},
		"keys": func(m any) []string {
			// sorry, i couldn't figure out how to do this...
			var keys []string
			switch temp := m.(type) {
			case map[string]map[string]string:
				for key := range temp {
					keys = append(keys, key)
				}
			case map[string]string:
				for key := range temp {
					keys = append(keys, key)
				}
			}
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
	data["name"] = "SUGMAASE"
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

	tok, err := c.Cookie("auth_token")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	claims, err := getClaimsFromToken(tok)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	isAdmin := claims["UserInfo"].(map[string]any)["Admin"].(bool)
	var team []TeamData
	if isAdmin {
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
		team[0], err = dbGetTeamScore(int(claims["UserInfo"].(map[string]any)["ID"].(float64)))
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
	tmpl.ParseFiles("templates/inject_drilldown.html")
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
	buf := new(bytes.Buffer)
	encoder := toml.NewEncoder(buf)
	encoder.Indent = "    "
	prettyconfig := eventConf // copy over primitives https://stackoverflow.com/questions/51635766/how-do-i-copy-a-struct
	var boxes []Box
	for _, box := range eventConf.Box {
		boxes = append(boxes, Box{
			Name:      box.Name,
			IP:        box.IP,
			CheckList: box.CheckList,
		})
	}
	prettyconfig.Box = boxes // swap for pretty formatted boxes
	if err := encoder.Encode(prettyconfig); err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	err = tmpl.Execute(c.Writer, pageData(c, gin.H{"title": "Engine", "status": !enginePause, "teams": teams, "prettyconfig": buf, "adjustments": adjustments})) // intuitively easier to pass in a true value for running
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
}
