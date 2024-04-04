package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

var (
	// Adjustments to process
	manualAdjustments map[uint]int

	engineMutex = &sync.Mutex{}

	stream = NewSSEServer()
)

func addViewRoutes(router *gin.RouterGroup) {
	router.GET("/", viewIndex)
	router.GET("/login", viewLogin)
	router.GET("/scoreboard", viewScoreboard) // need to implement public headtoheads
}

func addViewRoutesTeam(router *gin.RouterGroup) {
	router.GET("/announcements", viewAnnouncements)
	router.GET("/injects", viewInjects)
	router.GET("/injects/:injectid", viewInject)
	if eventConf.EasyPCR {
		router.GET("/pcrs", viewPCRs)
	}
	router.GET("/overview", viewOverview)
	if !eventConf.DisableHeadToHead {
		router.Static("/plots", "./plots")
	}
}

func addViewRoutesAdmin(router *gin.RouterGroup) {
	router.GET("/engine", viewEngine)
	if eventConf.DisableHeadToHead {
		router.Static("/plots", "./plots")
	}
	if !eventConf.EasyPCR {
		router.GET("/pcrs", viewPCRs)
	}
}

// POST routes have structs defined to specifically handle their received data
// These "form" structs will then be mapped to the internal database struct and used internally

func addPublicRoutes(router *gin.RouterGroup) {
	// authentication
	router.POST("/login", login)
}
func addAuthRoutes(router *gin.RouterGroup) {
	// sse
	router.GET("/sse", stream.ServeHTTP(), sse)

	// authentication
	router.GET("/logout", logout)

	// team portal
	router.GET("/teams/:teamid/scores/uptime", getTeamUptime)
	router.GET("/teams/:teamid/scores/sla", getTeamSLA)
	router.GET("/teams/:teamid/scores/rounds/:count", getTeamRounds) // maybe turn this into a get parameter
	router.GET("/teams/:teamid/scores/:servicename", getTeamService)

	// inject portal
	router.GET("/injects", getInjects)

	router.GET("/injects/:injectid", getInject)
	router.GET("/injects/:injectid/file/:filename", downloadInjectFile)
	router.POST("/injects/:injectid/submit", submitInject)
	router.GET("/injects/:injectid/:teamid", getTeamInjectSubmissions)
	router.GET("/injects/:injectid/:teamid/submissions/:submissionid/:filename", downloadSubmissionFile)

	// pcr portal
	router.POST("/pcrs/submit", submitPCR)
}
func addAdminRoutes(router *gin.RouterGroup) {
	// announcements
	router.POST("/announcements/add", addAnnouncement)
	router.DELETE("/announcements/:announcementid", deleteAnnouncement)

	// team portal
	router.POST("/teams/:teamid/edit", updateTeam)
	router.DELETE("/teams/:teamid", deleteTeam) // admin

	// admin portal
	router.GET("/engine/export/scores", exportScores) // admin
	router.GET("/engine/export/config", exportConfig) // admin

	router.GET("/engine/config", getConfig)    // admin
	router.PUT("/engine/config", submitConfig) // admin
	router.POST("/engine/addteam", addTeam)    // admin
	router.POST("/engine/adjustment", submitManualAdjustment)
	router.POST("/engine/pause", pauseEngine)
	router.POST("/engine/resume", resumeEngine)
	router.POST("/engine/reset", resetEngine)
	router.GET("/engine/services/:servicename", getServiceConfig)
	router.POST("/engine/syncldap", syncLdap)

	// inject portal
	router.POST("/injects/add", addInject)                                                               // admin
	router.POST("/injects/:injectid/edit", updateInject)                                                 // admin
	router.DELETE("/injects/:injectid", deleteInject)                                                    // admin
	router.POST("/injects/:injectid/:teamid/submissions/:submissionid/grade", gradeTeamInjectSubmission) // admin
}

func pauseEngine(c *gin.Context) {
	engineMutex.Lock()
	if enginePause {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Engine already paused"})
		engineMutex.Unlock()
		return
	}
	enginePauseWg.Add(1)
	enginePause = true
	engineMutex.Unlock()
	log.Println("[ENGINE] ===== Engine paused")
	SendSSE(gin.H{"admin": true, "page": "engine", "engine": false})
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func resumeEngine(c *gin.Context) {
	engineMutex.Lock()
	if !enginePause {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Engine already running"})
		engineMutex.Unlock()
		return
	}
	enginePauseWg.Done()
	enginePause = false
	engineMutex.Unlock()
	log.Println("[ENGINE] ===== Engine resumed")
	SendSSE(gin.H{"admin": true, "page": "engine", "engine": true})
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func resetEngine(c *gin.Context) {
	// reset round number
	log.Println("[ENGINE] ===== Event reset issued")
	engineMutex.Lock()
	initialEnginePause := enginePause // if engine was paused, stay paused
	enginePause = true

	for _, teamMap := range credentialsMutex {
		for _, credlist := range teamMap {
			credlist.Lock()
		}
	}

	// delete db data
	log.Println("[ENGINE] ===== Deleting database data")
	err := dbResetScoring()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// delete inject submissions too?

	// reset globals after all db transactions were successful
	roundNumber = 0
	enginePause = initialEnginePause

	log.Println("[ENGINE] ===== Deleting PCRs")
	for teamid, teamMap := range credentialsMutex {
		for _, credlist := range teamMap {
			os.RemoveAll(filepath.Join("submissions/pcrs", fmt.Sprint(teamid)))
			credlist.Unlock()
		}
	}

	log.Println("[ENGINE] ===== Deleting inject submissions")
	submissionDir, err := os.ReadDir("submissions")
	if err != nil {
		log.Fatalln("Failed to open submissions directory:", err)
	}
	for _, file := range submissionDir {
		if file.IsDir() && file.Name() != "pcrs" {
			os.RemoveAll(filepath.Join("submissions", file.Name()))
		}
	}

	log.Println("[ENGINE] ===== Deleting graphs")
	plotDir, err := os.ReadDir("plots")
	if err != nil {
		log.Fatalln("Failed to open plots directory:", err)
	}
	for _, file := range plotDir {
		if strings.HasSuffix(file.Name(), ".png") {
			os.Remove(filepath.Join("plots", file.Name()))
		}
	}

	log.Println("[ENGINE] ===== Reinitializing engine")
	bootstrap()

	engineMutex.Unlock()
	log.Println("[ENGINE] ===== Event reset successfully")
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func syncLdap(c *gin.Context) {
	if eventConf.LdapConnectUrl != "" {
		err := dbLoadLdapTeams()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		debugPrint("Synced LDAP teams to DB")
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

func getServiceConfig(c *gin.Context) {
	servicename := c.Param("servicename")
	for _, box := range eventConf.Box {
		for _, s := range box.Custom {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Dns {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Ftp {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Imap {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Ldap {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Ping {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Pop3 {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Rdp {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Smb {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Smtp {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Sql {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Ssh {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Tcp {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Vnc {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.Web {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
		for _, s := range box.WinRM {
			if s.Name == servicename {
				c.JSON(http.StatusOK, s)
				return
			}
		}
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "Service not found"})
}

// func updateServiceConfig(c *gin.Context) {
// 	type ServiceForm struct {
// 		Name         string    `json:"name"`    // Name is the box name plus the service (ex. lunar-dns)
// 		Display      string    `json:"display"` // Display is the name of the service (ex. dns)
// 		FQDN         string    `json:"fqdn"`
// 		IP           string    `json:"ip"`
// 		CredLists    []string  `json:"credlists"`
// 		Port         int       `json:"port"`
// 		Anonymous    bool      `json:"anonymous"`
// 		Points       int       `json:"points"`
// 		SlaPenalty   int       `json:"slapenalty"`
// 		SlaThreshold int       `json:"slathreshold"`
// 		LaunchTime   time.Time `json:"launchtime"`
// 		StopTime     time.Time `json:"stoptime"`
// 		Disabled     bool      `json:"disabled"`
// 	}

// 	var serviceForm ServiceForm
// 	if err := c.ShouldBindJSON(&serviceForm); err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
// 		return
// 	}

// 	servicename := c.Param("servicename")
// 	for _, box := range eventConf.Box {
// 		for _, service := range box.CheckList {
// 			if service.ServiceName == servicename {
// 				service.Service = serviceForm.Service
// 				c.JSON(http.StatusOK, service.Service)
// 				return
// 			}
// 		}
// 	}
// 	c.JSON(http.StatusBadGateway, gin.H{"error": "Service not found"})
// }

func getConfig(c *gin.Context) {
	c.JSON(http.StatusOK, eventConf)
}

func getTeamScore(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	teamScore, err := dbGetTeamScore(teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	teamUptime := make(map[string]Uptime)
	for _, check := range teamScore.Checks {
		teamUptime[check.ServiceName] = uptime[uint(teamid)][check.ServiceName]
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "scores": teamScore, "uptime": teamUptime})
}

func exportScores(c *gin.Context) {
	teams, err := dbGetTeams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type ScoreSummary struct {
		ID              uint
		Name            string
		ServiceTotal    int
		AdjustmentTotal int
		InjectTotal     int
		SLATotal        int
	}
	var export []ScoreSummary
	for _, team := range teams {
		score, err := dbGetTeamScore(int(team.ID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var adjustmentTotal int
		for _, adjustment := range score.ManualAdjustments {
			adjustmentTotal += adjustment.Amount
		}
		var injectTotal int
		for _, submission := range score.SubmissionData {
			injectTotal += submission.Score
		}
		var slaTotal int
		for _, sla := range score.SLAs {
			slaTotal += sla.Penalty
		}
		export = append(export, ScoreSummary{ID: team.ID, Name: team.Name, ServiceTotal: score.CumulativeServiceScore, AdjustmentTotal: adjustmentTotal, InjectTotal: injectTotal, SLATotal: slaTotal})
	}
	jsonData, err := json.MarshalIndent(gin.H{"export": export}, "", "    ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	exportPath := "./temporary/scores.json"
	os.WriteFile(exportPath, jsonData, 0644)
	c.File(exportPath)
}

func exportConfig(c *gin.Context) {
	buf := new(bytes.Buffer)
	encoder := toml.NewEncoder(buf)
	encoder.Indent = "    "
	if err := encoder.Encode(eventConf); err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	exportPath := "./temporary/export.conf"
	os.WriteFile(exportPath, buf.Bytes(), 0644)
	c.File(exportPath)
}

func submitConfig(c *gin.Context) {
	var configForm Config

	// Read the JSON data from the request body
	if err := c.ShouldBindJSON(&configForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := checkConfig(&configForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": strings.Split(err.Error(), "\n")})
		return
	}
	configForm.Box = eventConf.Box
	c.JSON(http.StatusOK, configForm)
}

func submitPCR(c *gin.Context) {
	type PCRForm struct {
		TeamID   int    `json:"teamid"`
		CredList string `json:"credlist"`
		Changes  string `json:"changes"`
	}

	var pcrForm PCRForm
	if err := c.ShouldBindJSON(&pcrForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && !eventConf.EasyPCR {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var teamid uint
	if claims.Admin {
		teamid = uint(pcrForm.TeamID)
	} else {
		teamid = claims.ID
	}

	scanner := bufio.NewScanner(strings.NewReader(pcrForm.Changes))
	for scanner.Scan() {
		record := strings.SplitN(scanner.Text(), ",", 2)
		// Process each line as needed
		if _, ok := credentials[pcrForm.CredList][teamid][record[0]]; ok {
			credentials[pcrForm.CredList][teamid][record[0]] = record[1]
		}
	}

	teamSpecificCredlist := filepath.Join("submissions/pcrs", fmt.Sprint(teamid), pcrForm.CredList)

	// Write the modified content back to the file
	credentialsMutex[pcrForm.CredList][teamid].Lock()
	file, err := os.Create(teamSpecificCredlist)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer file.Close()

	for username, password := range credentials[pcrForm.CredList][teamid] {
		_, err = file.WriteString(fmt.Sprintf("%s,%s\n", username, password))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	credentialsMutex[pcrForm.CredList][teamid].Unlock()

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func addAnnouncement(c *gin.Context) {
	type AnnouncementForm struct {
		Content string `json:"content"`
	}
	var announcementForm AnnouncementForm

	// Read the JSON data from the request body
	if err := c.ShouldBindJSON(&announcementForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if announcementForm.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing announcement content"})
		return
	}

	announcement := AnnouncementData{
		Content: announcementForm.Content,
	}
	_, err := dbAddAnnouncement(announcement)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	SendSSE(gin.H{"admin": false, "page": "announcements"})
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func deleteAnnouncement(c *gin.Context) {
	announcementid, _ := strconv.Atoi(c.Param("announcementid"))

	err := dbDeleteAnnouncement(announcementid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func addTeam(c *gin.Context) {
	type TeamForm struct {
		Name       string `json:"name"`
		Pw         string `json:"password"`
		Identifier string `json:"identifier"`
		//Token string `toml:"token,omitempty" json:"token,omitempty"`
	}
	var teamForm TeamForm

	// Read the JSON data from the request body
	if err := c.ShouldBindJSON(&teamForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if teamForm.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing team name"})
		return
	}
	if teamForm.Pw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing team password"})
		return
	}
	if teamForm.Identifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing team third octet"})
		return
	}
	identifier, err := strconv.Atoi(teamForm.Identifier)
	if identifier < 0 || identifier > 254 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team third octet"})
		return
	}

	team := TeamData{
		Name:       teamForm.Name,
		Pw:         teamForm.Pw,
		Identifier: teamForm.Identifier,
	}
	_, err = dbAddTeam(team)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Team name/IP must be unique"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "team": team})
}

func updateTeam(c *gin.Context) {
	// optional fields, only update ones that are not zero-valued
	type TeamForm struct {
		Name       string `form:"name"`
		Password   string `form:"password"`
		Identifier string `form:"identifier"`
	}
	var teamForm TeamForm
	teamid, _ := strconv.Atoi(c.Param("teamid"))

	if err := c.ShouldBindJSON(&teamForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	team := TeamData{
		ID: uint(teamid),
	}

	if teamForm.Name != "" {
		team.Name = teamForm.Name
	}

	if teamForm.Password != "" {
		team.Pw = teamForm.Password
	}

	if teamForm.Identifier != "" {
		team.Identifier = teamForm.Identifier
	}

	err := dbUpdateTeam(team)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Team name/IP must be unique"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func deleteTeam(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))

	err := dbDeleteTeam(teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func addInject(c *gin.Context) {
	type InjectForm struct {
		Title       string                  `form:"title" binding:"required"`
		Description string                  `form:"description" binding:"required"`
		OpenTime    string                  `form:"opentime" binding:"required"`
		DueTime     string                  `form:"duetime" binding:"required"`
		CloseTime   string                  `form:"closetime" binding:"required"`
		Files       []*multipart.FileHeader `form:"files" binding:"required"`
	}
	var injectForm InjectForm

	// Read the form data from the request body
	if err := c.ShouldBind(&injectForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// realistically only error should be bad format
	ot, err := time.Parse(time.RFC3339, injectForm.OpenTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing open time or wrong format"})
		return
	}
	dt, err := time.Parse(time.RFC3339, injectForm.DueTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing due time or wrong format"})
		return
	}
	ct, err := time.Parse(time.RFC3339, injectForm.CloseTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing close time or wrong format"})
		return
	}

	if ot.After(dt) || dt.After(ct) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Open time must be before due time, and due time must be before close time"})
		return
	}

	var filenames []string
	for _, fileHeader := range injectForm.Files {
		filenames = append(filenames, filepath.Base(fileHeader.Filename))
		dst := filepath.Join("./injects", injectForm.Title, filepath.Base(fileHeader.Filename))
		if err := c.SaveUploadedFile(fileHeader, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
			return
		}
	}

	inject := InjectData{
		Title:           injectForm.Title,
		Description:     injectForm.Description,
		OpenTime:        ot.Truncate(time.Minute),
		DueTime:         dt.Truncate(time.Minute),
		CloseTime:       ct.Truncate(time.Minute),
		InjectFileNames: filenames,
	}

	injectid, err := dbAddInject(inject)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Inject name must be unique"})
			return
		}
		// Delete uploaded files if database function fails
		os.RemoveAll(filepath.Join("./injects", injectForm.Title))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	teams, err := dbGetTeams() // consider creating teams map in memory to avoid database query
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	submissionMutex.Lock()
	submissions[int(injectid)] = make(map[int]int)
	for _, team := range teams {
		submissions[int(injectid)][int(team.ID)] = 0
	}
	submissionMutex.Unlock()
	SendSSE(gin.H{"admin": true, "page": "injects"})
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func getInjects(c *gin.Context) {
	injects, err := dbGetInjects()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// TODO: if Admin return, otherwise remove injects not opened yet

	c.JSON(http.StatusOK, injects)
}

func getInject(c *gin.Context) {
	injectid, _ := strconv.Atoi(c.Param("injectid"))

	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// TODO: if Admin return, otherwise remove injects not opened yet

	c.JSON(http.StatusOK, inject)
}

// currently does not support updating changing ID
func updateInject(c *gin.Context) {
	// optional fields, only update ones that are not zero-valued
	type InjectForm struct {
		Title       string                  `form:"title"`
		Description string                  `form:"description"`
		OpenTime    string                  `form:"opentime"`
		DueTime     string                  `form:"duetime"`
		CloseTime   string                  `form:"closetime"`
		Files       []*multipart.FileHeader `form:"files"`
	}
	var injectForm InjectForm
	injectid, _ := strconv.Atoi(c.Param("injectid"))

	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.ShouldBind(&injectForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if injectForm.Title != "" {
		inject.Title = injectForm.Title
	}

	if injectForm.Description != "" {
		inject.Description = injectForm.Description
	}

	if injectForm.OpenTime != "" {
		ot, err := time.Parse(time.RFC3339, injectForm.OpenTime)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Open time wrong format"})
			return
		}
		inject.OpenTime = ot.Truncate(time.Minute)
	}

	if injectForm.DueTime != "" {
		dt, err := time.Parse(time.RFC3339, injectForm.DueTime)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Due time wrong format"})
			return
		}
		inject.DueTime = dt.Truncate(time.Minute)
	}

	if injectForm.CloseTime != "" {
		ct, err := time.Parse(time.RFC3339, injectForm.CloseTime)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Close time wrong format"})
			return
		}
		inject.CloseTime = ct.Truncate(time.Minute)
	}

	if inject.OpenTime.After(inject.DueTime) || inject.DueTime.After(inject.CloseTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Open time must be before due time, and due time must be before close time"})
		return
	}

	if len(injectForm.Files) != 0 {
		os.RemoveAll(filepath.Join("./injects", injectForm.Title))
		var filenames []string
		for _, fileHeader := range injectForm.Files {
			filenames = append(filenames, filepath.Base(fileHeader.Filename))
			dst := filepath.Join("./injects", injectForm.Title, filepath.Base(fileHeader.Filename))
			if err := c.SaveUploadedFile(fileHeader, dst); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
				return
			}
		}
		inject.InjectFileNames = filenames
	}

	err = dbUpdateInject(inject)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Inject name must be unique"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func submitInject(c *gin.Context) {
	type InjectSubmissionForm struct {
		Files []*multipart.FileHeader `form:"files" binding:"required"`
	}
	var submissionForm InjectSubmissionForm
	submissionTime := time.Now()
	injectid, _ := strconv.Atoi(c.Param("injectid"))

	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if submissionTime.After(inject.CloseTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Inject '%s' cannot accept submission after its close time", inject.Title)})
		return
	}

	if err := c.ShouldBind(&submissionForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	teamid := int(claims.ID)
	submissionid := submissions[injectid][teamid] + 1

	var filenames []string
	for _, fileHeader := range submissionForm.Files {
		filenames = append(filenames, filepath.Base(fileHeader.Filename))
		dst := filepath.Join("./submissions", inject.Title, fmt.Sprint(teamid), fmt.Sprint("attempt", fmt.Sprint(submissionid)), filepath.Base(fileHeader.Filename))
		if err := c.SaveUploadedFile(fileHeader, dst); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
			return
		}
	}

	submission := SubmissionData{
		TeamID:              uint(teamid),
		InjectID:            uint(injectid),
		SubmissionTime:      submissionTime,
		SubmissionFileNames: filenames,
		AttemptNumber:       submissionid,
	}

	err = dbSubmitInject(submission)

	if err != nil {
		// Delete uploaded files if database function fails
		os.RemoveAll(filepath.Join("./submissions", fmt.Sprint(injectid), fmt.Sprint(teamid), fmt.Sprint("attempt", fmt.Sprint(submissionid))))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	submissionMutex.Lock()
	submissions[injectid][teamid] = submissionid
	submissionMutex.Unlock()

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// consider preventing how inject deletion might work during competition after submissions have been made
func deleteInject(c *gin.Context) {
	injectid, _ := strconv.Atoi(c.Param("injectid"))

	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	err = dbDeleteInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	os.RemoveAll(filepath.Join("./injects", inject.Title))
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func getTeamInjectSubmissions(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	injectid, _ := strconv.Atoi(c.Param("injectid"))

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	teamInjectSubmissions, err := dbGetInjectSubmissions(injectid, teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "submissions": teamInjectSubmissions})
}

// grader
// score
// feedback
func gradeTeamInjectSubmission(c *gin.Context) {
	type SubmissionForm struct {
		Grader   string `json:"grader"`   // required will be handled by admin frontend here
		Score    int    `json:"score"`    // required will be handled by admin frontend here
		Feedback string `json:"feedback"` // required will be handled by admin frontend here
	}
	var gradedSubmissionForm SubmissionForm

	teamid, _ := strconv.Atoi(c.Param("teamid"))
	injectid, _ := strconv.Atoi(c.Param("injectid"))
	submissionid, _ := strconv.Atoi(c.Param("submissionid"))

	if err := c.ShouldBindJSON(&gradedSubmissionForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	gradedSubmission := SubmissionData{
		TeamID:        uint(teamid),
		InjectID:      uint(injectid),
		AttemptNumber: submissionid,
		Grader:        gradedSubmissionForm.Grader,
		Score:         gradedSubmissionForm.Score,
		Feedback:      gradedSubmissionForm.Feedback,
	}

	err := dbGradeInjectSubmission(gradedSubmission)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func downloadInjectFile(c *gin.Context) {
	injectid, _ := strconv.Atoi(c.Param("injectid"))
	filename := c.Param("filename")
	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if claims.Admin == false && time.Now().Before(inject.OpenTime) {
		c.JSON(http.StatusNotFound, gin.H{"error": "that file does not exist!"})
		return
	}

	for _, name := range inject.InjectFileNames {
		if filename == name {
			filename = filepath.Join("./injects", inject.Title, filepath.Base(filename))
			c.File(filename)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "that file does not exist!"})
}

func downloadSubmissionFile(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	injectid, _ := strconv.Atoi(c.Param("injectid"))
	submissionid, _ := strconv.Atoi(c.Param("submissionid"))
	filename := c.Param("filename")

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	submissions, err := dbGetInjectSubmissions(injectid, teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	inject, err := dbGetInject(injectid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	for _, submission := range submissions {
		if submission.AttemptNumber == submissionid {
			for _, name := range submission.SubmissionFileNames {
				if filename == name {
					filename = filepath.Join("./submissions", inject.Title, fmt.Sprint(teamid), fmt.Sprint("attempt", fmt.Sprint(submissionid)), filepath.Base(filename))
					c.File(filename)
					return
				}
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "that file does not exist!"})
			return
		}
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
}

func submitManualAdjustment(c *gin.Context) {
	type AdjustmentForm struct {
		TeamID int    `json:"teamid"`
		Amount int    `json:"amount"`
		Reason string `json:"reason"`
	}
	var adjustmentForm AdjustmentForm

	if err := c.ShouldBindJSON(&adjustmentForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}

	adjustment := ManualAdjustmentData{
		TeamID: uint(adjustmentForm.TeamID),
		Amount: adjustmentForm.Amount,
		Reason: adjustmentForm.Reason,
	}

	err := dbSubmitManualAdjustment(adjustment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func getTeamUptime(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	teamUptime := make(map[string]Uptime)
	for _, box := range eventConf.Box {
		for _, runner := range box.Runners {
			if time.Now().After(runner.GetService().LaunchTime) {
				teamUptime[runner.GetService().Name] = uptime[uint(teamid)][runner.GetService().Name]
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"uptime": teamUptime})
}

func getTeamSLA(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	slas, err := dbGetTeamSLAs(teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"slas": slas})
}

func getTeamRounds(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	count, err := strconv.Atoi(c.Param("count"))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	rounds, err := dbGetTeamRounds(teamid, count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rounds": rounds})
}

func getTeamService(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	servicename := c.Param("servicename")

	// team based auth
	claims, err := contextGetClaims(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !claims.Admin && claims.ID != uint(teamid) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	servicedata, err := dbGetTeamServices(teamid, -1, servicename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !claims.Admin && !eventConf.Verbose {
		for i := range servicedata {
			// use i to edit slice in place
			servicedata[i].Debug = ""
			servicedata[i].Error = ""
		}
	}
	c.JSON(http.StatusOK, servicedata)
}
