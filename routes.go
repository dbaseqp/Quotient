package main

import (
	"bufio"
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

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

var (
	// Adjustments to process
	manualAdjustments map[uint]int

	engineMutex = &sync.Mutex{}
)

func addViewRoutes(router *gin.RouterGroup) {
	router.GET("/", viewIndex)
	router.GET("/login", viewLogin)
}

func addViewRoutesTeam(router *gin.RouterGroup) {
	router.GET("/scoreboard", viewScoreboard) // need to implement public headtoheads
	router.GET("/announcements", viewAnnouncements)
	router.GET("/injects", viewInjects)
	router.GET("/injects/:injectid", viewInject)
	router.GET("/pcrs", viewPCRs)
	router.GET("/overview", viewOverview)
	if eventConf.DisableHeadToHead == false {
		router.Static("/plots", "./plots")
	}
}

func addViewRoutesAdmin(router *gin.RouterGroup) {
	router.GET("/engine", viewEngine)
	if eventConf.DisableHeadToHead == true {
		router.Static("/plots", "./plots")
	}
}

// POST routes have structs defined to specifically handle their received data
// These "form" structs will then be mapped to the internal database struct and used internally

func addPublicRoutes(router *gin.RouterGroup) {
	// authentication
	router.POST("/login", login)
}
func addAuthRoutes(router *gin.RouterGroup) {
	// authentication
	router.GET("/logout", logout)

	// team portal
	router.GET("/teams/:teamid/scores", getTeamScore)
	router.GET("/teams/:teamid/scores/:servicename", getTeamService)

	// inject portal
	router.GET("/injects", getInjects)

	router.GET("/injects/:injectid", getInject)
	router.GET("/injects/:injectid/file/:filename", downloadInjectFile)
	router.POST("/injects/:injectid/submit", submitInject)
	router.GET("/injects/:injectid/:teamid", getTeamInjectSubmissions)

	// pcr portal
	router.POST("/pcrs/submit", submitPCR)
}
func addAdminRoutes(router *gin.RouterGroup) {
	// team portal
	router.POST("/teams/:teamid/edit", updateTeam)
	router.DELETE("/teams/:teamid", deleteTeam) // admin

	// admin portal
	router.GET("/engine/export", exportScores) // admin
	router.GET("/engine/config", getConfig)    // admin
	router.PUT("/engine/config", submitConfig) // admin
	router.POST("/engine/addteam", addTeam)    // admin
	router.POST("/engine/adjustment", submitManualAdjustment)
	router.POST("/engine/announcements/add", addAnnouncement)
	router.DELETE("/engine/announcements/:announcementid", deleteAnnouncement)
	router.POST("/engine/pause", pauseEngine)
	router.POST("/engine/resume", resumeEngine)
	router.POST("/engine/reset", resetEngine)

	// inject portal
	router.POST("/injects/:injectid/:teamid/submissions/:submissionid/grade", gradeTeamInjectSubmission) // admin
	router.GET("/injects/:injectid/:teamid/submissions/:submissionid/:filename", downloadSubmissionFile) //admin
	router.POST("/injects/add", addInject)                                                               // admin
	router.POST("/injects/:injectid/edit", updateInject)                                                 // admin
	router.DELETE("/injects/:injectid", deleteInject)                                                    // admin
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
	c.JSON(http.StatusOK, teamScore)
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
	exportPath := "./temporary/export.json"
	os.WriteFile(exportPath, jsonData, 0644)
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

	var pcrForm PCRForm
	if err := c.ShouldBindJSON(&pcrForm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var teamid uint
	userinfo := claims["UserInfo"].(map[string]any)
	if userinfo["Admin"].(bool) {
		teamid = uint(pcrForm.TeamID)
	} else {
		teamid = uint(userinfo["ID"].(float64))
	}

	scanner := bufio.NewScanner(strings.NewReader(pcrForm.Changes))
	for scanner.Scan() {
		record := strings.Split(scanner.Text(), ",")
		// Process each line as needed
		if _, ok := credentials[teamid][pcrForm.CredList][record[0]]; ok {
			credentials[teamid][pcrForm.CredList][record[0]] = record[1]
		}
	}

	teamSpecificCredlist := filepath.Join("submissions/pcrs", fmt.Sprint(teamid), pcrForm.CredList)

	// Write the modified content back to the file
	credentialsMutex[teamid][pcrForm.CredList].Lock()
	file, err := os.Create(teamSpecificCredlist)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer file.Close()

	for username, password := range credentials[teamid][pcrForm.CredList] {
		_, err = file.WriteString(fmt.Sprintf("%s,%s", username, password))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	credentialsMutex[teamid][pcrForm.CredList].Unlock()

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
		Name string `json:"name"`
		Pw   string `json:"password"`
		IP   int    `json:"ip"` // 3rd octet
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
	if teamForm.IP == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing team third octet"})
		return
	}
	if teamForm.IP < 1 || teamForm.IP > 254 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid team third octet"})
		return
	}

	team := TeamData{
		Name: teamForm.Name,
		Pw:   teamForm.Pw,
		IP:   teamForm.IP,
	}
	_, err := dbAddTeam(team)
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

func updateTeam(c *gin.Context) {
	// optional fields, only update ones that are not zero-valued
	type TeamForm struct {
		Name     string `form:"name"`
		Password string `form:"password"`
		IP       int    `form:"ip"`
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

	if teamForm.IP != 0 {
		team.IP = teamForm.IP
	}

	err := dbUpdateTeam(team)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Team name/IP must be unique")})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Inject name must be unique")})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Inject name must be unique")})
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
	teamid := int(claims["UserInfo"].(map[string]any)["ID"].(float64))
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
	teamInjectSubmissions, err := dbGetInjectSubmissions(injectid, teamid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, teamInjectSubmissions)
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

	if isAdmin == false && time.Now().Before(inject.OpenTime) {
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

// perform authorization check and then move to auth routes
func downloadSubmissionFile(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	injectid, _ := strconv.Atoi(c.Param("injectid"))
	submissionid, _ := strconv.Atoi(c.Param("submissionid"))
	filename := c.Param("filename")
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

func getTeamService(c *gin.Context) {
	teamid, _ := strconv.Atoi(c.Param("teamid"))
	servicename := c.Param("servicename")
	servicedata, err := dbGetTeamServices(teamid, -1, servicename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, servicedata)
}
