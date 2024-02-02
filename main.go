package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sugmaase/checks"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// globals
var (
	eventConf = Config{}
	db        = &gorm.DB{}

	startTime time.Time

	configPath = flag.String("c", "config/event.conf", "configPath")
	debug      = flag.Bool("d", false, "debugFlag")

	roundNumber int
	resetIssued bool
	loc         *time.Location
	// ct          CredentialTable

	slaCount = make(map[uint]map[string]int) // slaCount[teamid][servicename] = slacount
	/*
		Track submission counts in memory because counting in DB after every submission is expensive

		submissions[injectid][teamid]
	*/
	submissions     = make(map[int]map[int]int)
	submissionMutex = &sync.Mutex{}

	/*
		Map to store state of credential sets

		credentials[teamid][listname][username] = password
	*/
	credentials      = make(map[uint]map[string]map[string]string)
	credentialsMutex = make(map[uint]map[string]*sync.Mutex)

	enginePauseWg = &sync.WaitGroup{}
	enginePause   bool
	pauseTime     time.Time

	teamMutex       = &sync.Mutex{}
	persistMutex    = &sync.Mutex{}
	agentMutex      = &sync.Mutex{}
	adjustmentMutex = &sync.Mutex{}
)

func init() {
	flag.Parse()
}

func main() {
	now := time.Now()
	loadConfigs()
	bootstrap()
	startEvent(now)
}

func loadConfigs() {
	if _, err := os.Stat(*configPath); errors.Is(err, os.ErrNotExist) {
		file, err := os.Create(*configPath)
		if err != nil {
			log.Fatalln(errors.Wrap(err, "failed to create event config"))
		}
		defer file.Close()
	}
	eventConf = readConfig(*configPath)

	err := checkConfig(&eventConf)
	if err != nil {
		log.Fatalln(errors.Wrap(err, "illegal config"))
	}
}

func bootstrap() {
	var err error // this way we can avoid using := for below statement and use global "db"
	db, err = gorm.Open(postgres.Open(eventConf.DBConnectURL), &gorm.Config{TranslateError: true})
	if err != nil {
		log.Fatalln("Failed to connect database!")
	}
	debugPrint("Connected to DB")

	err = db.AutoMigrate(&TeamData{}, &RoundData{}, &ServiceData{}, &SLAData{}, &ManualAdjustmentData{}, &InjectData{}, &SubmissionData{}, &AnnouncementData{}, &RoundPointsData{})
	if err != nil {
		log.Fatalln("Failed to auto migrate:", err)
		return
	}

	err = dbCalculateCumulativeServiceScore()
	if err != nil {
		log.Fatalln("Failed to calculate cumulative service scores:", err)
	}
	debugPrint("Recalculated cumulative service score caches")

	submissions, err = dbLoadSubmissions()
	if err != nil {
		log.Fatalln("Failed to load submissions:", err)
	}
	debugPrint("Loaded submissions into memory")

	roundNumber, err = dbGetLastRoundNumber()
	if err != nil {
		log.Fatalln("Failed to load previous round data:", err)
	}
	roundNumber++
	//
	// // Initialize manual adjustments map
	// manualAdjustments = make(map[uint]int)
	//
	// // Load timezone
	loc, err = time.LoadLocation(eventConf.Timezone)
	if err != nil {
		log.Fatalln(errors.Wrap(err, "invalid timezone"))
	}

	privateKey, publicKey, err = readKeyFiles()
	if err != nil {
		log.Fatalln("Failed to load JWT keys:", err)
	}

	// Load SLA state for all teams' checks
	teams, err := dbGetTeams()
	if err != nil {
		log.Fatalln("Failed to load teams:", err)
	}

	for _, team := range teams {
		slaCount[team.ID] = make(map[string]int)
		results, err := dbGetTeamServices(int(team.ID), eventConf.SlaThreshold*2, "")
		if err != nil {
			log.Fatalln("Failed to load team score data:", err)
		}
		for _, box := range eventConf.Box {
			for _, check := range box.CheckList {
				for _, result := range results {
					if result.ServiceName == check.Name {
						if result.Result == false {
							slaCount[team.ID][check.Name]++
							if slaCount[team.ID][check.Name] == check.SlaThreshold {
								// an SLA was detected but it should already exist in DB, so reset
								slaCount[team.ID][check.Name] = 0
							}
						} else {
							slaCount[team.ID][check.Name] = 0
						}
					}
				}
			}
		}
	}
	debugPrint("Loaded SLA states into memory")

	credlistFiles, err := os.ReadDir("config")
	if err != nil {
		log.Fatalln("Failed to load credlist files:", err)
	}
	for _, team := range teams {
		credentials[team.ID] = make(map[string]map[string]string)
		credentialsMutex[team.ID] = make(map[string]*sync.Mutex)
		for _, file := range credlistFiles {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".credlist") {
				continue // Skip directories and non .credlist files
			}
			credentials[team.ID][file.Name()] = make(map[string]string)
			credentialsMutex[team.ID][file.Name()] = &sync.Mutex{}
			// flesh out default credlists to teams
			teamSpecificCredlist := filepath.Join("submissions/pcrs/", fmt.Sprint(team.ID), file.Name())
			_, err = os.Stat(teamSpecificCredlist)
			// if file doesn't exist
			if err != nil {
				debugPrint("No", file.Name(), "file found for", team.Name, "... creating default credlist")
				if err := os.MkdirAll(filepath.Join("submissions/pcrs/", fmt.Sprint(team.ID)), os.ModePerm); err != nil {
					log.Fatalln("Failed to create copy credlist for team:", team.ID, team.Name, err.Error())
				}

				credlistPath := filepath.Join("config", file.Name())
				credlist, err := os.Open(credlistPath)
				if err != nil {
					fmt.Println("Error opening file:", err)
					continue
				}
				defer credlist.Close()

				destination, err := os.Create(teamSpecificCredlist) //create the destination file
				if err != nil {
					log.Fatalln("Failed to create copy credlist for team:", team.ID, team.Name)
				}
				defer destination.Close()
				_, err = io.Copy(destination, credlist)
				if err != nil {
					log.Fatalln("Failed to copy credlist for team:", team.ID, team.Name)
				}
			}

			// Create a CSV reader
			credlist, err := os.Open(teamSpecificCredlist)
			if err != nil {
				fmt.Println("Error opening file:", err)
				continue
			}
			defer credlist.Close()
			reader := csv.NewReader(credlist)

			// Read the CSV data
			for {
				record, err := reader.Read()
				if err == io.EOF {
					break
				} else if err != nil {
					fmt.Println("Error reading CSV:", err)
					break
				}
				credentials[team.ID][file.Name()][record[0]] = record[1]
			}
		}
	}
	checks.Creds = credentials
	debugPrint("Loaded credential states into memory", credentials)

}

func startEvent(beginTime time.Time) {
	// Initialize router
	if !*debug {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.Default()
	router.StaticFile("/favicon.ico", "./assets/favicon.ico")
	router.Static("/assets", "./assets")
	initCookies(router)
	// possible fuzzing of inject files countered by sending pdf as base64

	// 404 handler
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"message": "why are you here"})
	})

	// Create router
	viewRoutesPublic := router.Group("/")
	addViewRoutes(viewRoutesPublic)

	viewRoutesTeam := router.Group("/")
	viewRoutesTeam.Use(authRequired)
	addViewRoutesTeam(viewRoutesTeam)

	viewRoutesAdmin := router.Group("/")
	viewRoutesAdmin.Use(adminAuthRequired)
	addViewRoutesAdmin(viewRoutesAdmin)

	publicAPIRoutes := router.Group("/api")
	addPublicRoutes(publicAPIRoutes)

	authAPIRoutes := router.Group("/api")
	addAuthRoutes(authAPIRoutes)

	adminAPIRoutes := router.Group("/api")
	addAdminRoutes(adminAPIRoutes)

	// Start the event
	if eventConf.StartPaused {
		pauseTime = time.Now()
		enginePause = true
		log.Println("Event started, but scoring will not begin automatically")
	} else {
	}

	// Run scoring algorithm
	go Score()

	// Start the web server
	log.Println("Startup complete. Took", time.Now().Sub(beginTime))
	if eventConf.Https {
		log.Fatal(router.RunTLS(fmt.Sprintf(":%d", eventConf.Port), eventConf.Cert, eventConf.Key))
	} else {
		log.Fatal(router.Run(fmt.Sprintf(":%d", eventConf.Port)))
	}
}
