package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"quotient/checks"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"golang.org/x/exp/slices"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// globals
var (
	eventConf = Config{}
	db        = &gorm.DB{}

	configPath = flag.String("c", "config/event.conf", "configPath")
	debug      = flag.Bool("d", false, "debugFlag")

	roundNumber int
	loc         *time.Location
	// ct          CredentialTable

	slaCount = make(map[uint]map[string]int)    // slaCount[teamid][servicename] = slacount
	uptime   = make(map[uint]map[string]Uptime) // slaCount[teamid][servicename] = uptime
	/*
		Track submission counts in memory because counting in DB after every submission is expensive

		submissions[injectid][teamid]
	*/
	submissions     = make(map[int]map[int]int)
	submissionMutex = &sync.Mutex{}

	/*
		Map to store state of credential sets

		credentials[listname][teamid][username] = password
	*/
	credentials      = make(map[uint]map[string]map[string]string)
	credentialsMutex = make(map[uint]map[string]*sync.Mutex)

	enginePauseWg = &sync.WaitGroup{}
	enginePause   bool
)

type Uptime struct {
	Ups   int
	Total int
}

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

	err = db.AutoMigrate(&TeamData{}, &RoundData{}, &CheckData{}, &SLAData{}, &ManualAdjustmentData{}, &InjectData{}, &SubmissionData{}, &AnnouncementData{}, &RoundPointsData{})
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

	round, err := dbGetLastRound()
	if err != nil {
		log.Fatalln("Failed to load previous round data:", err)
	}
	roundNumber = int(round.ID)
	roundNumber++

	// Load timezone
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
		results, err := dbGetTeamServices(int(team.ID), -1, "")
		if err != nil {
			log.Fatalln("Failed to load team score data:", err)
		}
		slices.Reverse(results)

		for _, box := range eventConf.Box {
			for _, runner := range box.Runners {
				for _, result := range results {
					if result.ServiceName == runner.GetService().Name {
						if result.Result == false {
							slaCount[team.ID][runner.GetService().Name]++
							if slaCount[team.ID][runner.GetService().Name] == runner.GetService().SlaThreshold {
								// an SLA was detected but it should already exist in DB, so reset
								slaCount[team.ID][runner.GetService().Name] = 0
							}
						} else {
							slaCount[team.ID][runner.GetService().Name] = 0
						}
					}
				}
			}
		}
	}
	debugPrint("Loaded SLA states into memory")

	for _, team := range teams {
		uptime[team.ID] = make(map[string]Uptime)
		results, err := dbGetTeamServices(int(team.ID), -1, "")
		if err != nil {
			log.Fatalln("Failed to load team all score data:", err)
		}
		for _, box := range eventConf.Box {
			for _, runner := range box.Runners {
				for _, result := range results {
					if result.ServiceName == runner.GetService().Name {
						service, ok := uptime[team.ID][runner.GetService().Name]
						if !ok {
							uptime[team.ID][runner.GetService().Name] = Uptime{}
						}
						if result.Result == true {
							service.Ups++
						}
						service.Total++
						uptime[team.ID][runner.GetService().Name] = service
					}
				}
			}
		}
	}
	debugPrint("Loaded uptime states into memory")

	credlistFiles, err := os.ReadDir("config")
	if err != nil {
		log.Fatalln("Failed to load credlist files:", err)
	}

	for _, team := range teams {
		credentials[team.ID] = make(map[string]map[string]string)
		credentialsMutex[team.ID] = make(map[string]*sync.Mutex)
		for _, file := range credlistFiles {
			if !strings.HasSuffix(file.Name(), ".credlist") {
				continue // Skip directories and non .credlist files
			}
			// team variation credlists
			if file.IsDir() {
				// this will generate clones of credlists even if team doesn't use it
				err := filepath.WalkDir(filepath.Join("config", file.Name()), func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".credlist") {
						return nil
					}

					if strings.Contains(entry.Name(), team.Identifier) {
						err := generateCredlist(filepath.Join(file.Name(), entry.Name()), entry.Name(), team)
						if err != nil {
							fmt.Println("Error opening file:", err)
						}
					}
					return nil
				})
				if err != nil {
					log.Fatalln("Failed to load credlist files:", err)
				}

			} else {
				generateCredlist(file.Name(), file.Name(), team)
			}
		}
	}
	checks.Creds = credentials
	debugPrint("Loaded credential states into memory", credentials)

	debugPrint("Initializing graphs")
	err = makeGraphs()
	if err != nil {
		errorPrint("FAILED TO MAKE GRAPHS FOR ROUND", roundNumber, ":", err.Error())
	}
	debugPrint("Finished generating graphs")
}

func generateCredlist(path string, name string, team TeamData) error {
	credentials[team.ID][name] = make(map[string]string)
	credentialsMutex[team.ID][name] = &sync.Mutex{}
	// flesh out default credlists to teams
	teamSpecificCredlist := filepath.Join("submissions/pcrs/", fmt.Sprint(team.ID), name)
	_, err := os.Stat(teamSpecificCredlist)
	// if file doesn't exist
	if err != nil {
		debugPrint("No", path, "file found for", team.Name, "... creating default credlist")
		if err := os.MkdirAll(filepath.Join("submissions/pcrs/", fmt.Sprint(team.ID)), os.ModePerm); err != nil {
			log.Fatalln("Failed to create copy credlist for team:", team.ID, team.Name, err.Error())
		}

		credlistPath := filepath.Join("config", path)
		credlist, err := os.Open(credlistPath)
		if err != nil {
			return err
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
		return err
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
		credentials[team.ID][name][record[0]] = record[1]
	}
	return nil
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
	authAPIRoutes.Use(authRequired)
	addAuthRoutes(authAPIRoutes)

	adminAPIRoutes := router.Group("/api")
	adminAPIRoutes.Use(adminAuthRequired)
	addAdminRoutes(adminAPIRoutes)

	// Start the event
	if eventConf.StartPaused {
		enginePause = true
		log.Println("Event started, but scoring will not begin automatically")
	}

	// Run scoring algorithm
	go Score()

	// Start the web server
	log.Println("Startup complete. Took", time.Since(beginTime))
	if eventConf.Https {
		log.Fatal(router.RunTLS(fmt.Sprintf("%s:%d", eventConf.BindAddress, eventConf.Port), eventConf.Cert, eventConf.Key))
	} else {
		log.Fatal(router.Run(fmt.Sprintf("%s:%d", eventConf.BindAddress, eventConf.Port)))
	}
}
