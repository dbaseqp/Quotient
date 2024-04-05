package main

import (
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"quotient/checks"
	"strings"
	"sync"
	"time"
)

var (
	roundStartTime time.Time
)

func Score() {
	// wait until engine is not paused
	for {
		// static state of eventConf so it is stable for duration of this round
		roundConf := eventConf

		log.Println("[SCORE] ===== Queuing round", roundNumber)
		enginePauseWg.Wait()

		// run this round
		log.Println("[SCORE] ===== Starting round", roundNumber)
		roundStartTime = time.Now()

		jitter := time.Duration(0)
		if eventConf.Jitter != 0 {
			jitter = time.Duration(time.Duration(rand.Intn(eventConf.Jitter+1)) * time.Second)
		}
		nextRoundTime := roundStartTime.Add((time.Duration(eventConf.Delay) * time.Second) + jitter)
		log.Println("[SCORE] ===== Next round scheduled after", eventConf.Delay, "seconds with jitter", jitter)

		var roundData map[uint][]checks.Result
		switch eventConf.EventType {
		case "rvb":
			roundData = scoreRvB(roundConf)
		case "koth":
			roundData = scoreKoTH(roundConf)
		}

		var err error
		// award teams for successful scores
		err = dbProcessRound(roundConf, roundStartTime, roundData)
		if err != nil {
			// if you see this, that is very bad
			errorPrint("FAILED TO SAVE ROUND DATA FOR ROUND", roundNumber, ":", err.Error())
		}

		// calculate total scores for each team
		err = dbUpdateCumulativeServiceScoreCache(roundData)
		if err != nil {
			errorPrint("FAILED TO UPDATE CUMULATIVE SCORE CACHE DATA FOR ROUND", roundNumber, ":", err.Error())
		}

		// detect service downage
		err = detectSLAs(roundConf, roundData)
		if err != nil {
			errorPrint("FAILED TO GENERATE SLA DATA FOR ROUND", roundNumber, ":", err.Error())
		}

		log.Println("[SCORE] ===== Ending for round", roundNumber)
		debugPrint("Round", roundNumber, "took", time.Since(roundStartTime).String(), "to finish")

		// prepare for next round
		sleepDuration := time.Until(nextRoundTime)
		log.Println("[SCORE] Sleeping for", sleepDuration, "seconds until next round")
		time.Sleep(sleepDuration)
		roundNumber++
		err = makeGraphs()
		if err != nil {
			errorPrint("FAILED TO MAKE GRAPHS FOR ROUND", roundNumber, ":", err.Error())
		}
	}
}

func scoreKoTH(m Config) map[uint][]checks.Result {
	// TODO
	// roundData := make(map[uint][]checks.Result)

	time.Sleep(5 * time.Second)
	return nil
}

func rotateIP(m Config) error {
	command := fmt.Sprintf("./scripts/rotate.sh %s %s %s %s", m.BindAddress, m.Gateway, m.Subnet, m.Interface)
	out, err := exec.Command("/bin/sh", "-c", command).Output()

	if err != nil {
		return err
	}
	output := strings.TrimSpace(string(out))
	debugPrint("rotated to", output)
	return nil
}

func scoreRvB(m Config) map[uint][]checks.Result {
	roundData := make(map[uint][]checks.Result)
	err := rotateIP(m)
	if err != nil {
		errorPrint("failed to rotate IPs")
	}

	teams, err := dbGetTeams()
	if err != nil {
		errorPrint("Unable to load teams for Round", roundNumber, ":", err.Error())
		return roundData // die?
	}

	allTeamsWg := &sync.WaitGroup{}

	// CONSIDER: defer + pass wait group to run function? this would simplify code, but would it be coupling too much functionality
	// run all the checks per box per team
	roundDataMutex := sync.Mutex{} // for concurrent map write protection
	for _, team := range teams {
		allTeamsWg.Add(1)
		go func(team TeamData) {
			teamRunners := make(chan checks.Result) // channel to handle checks finishing
			thisTeamWg := &sync.WaitGroup{}
			// spawn all the checks for this team then wait to process once all checks finish

			for _, box := range m.Box {
				for _, runner := range box.Runners {
					service := runner.GetService()
					if roundStartTime.Before(service.LaunchTime) || roundStartTime.After(service.StopTime) {
						continue
					}
					thisTeamWg.Add(1)
					debugPrint("[SCORE] Running", service.Name, "for", team.Name)
					go checks.Dispatch(team.ID, team.Identifier, box.Name, box.IP, box.FQDN, runner, teamRunners)
				}
			}

			// goroutine to detect team completion. located after checks have been spawned so channel doesn't close early
			teamCompleteChan := make(chan bool) // channel to handle this team finishing
			go func() {
				thisTeamWg.Wait()
				teamCompleteChan <- true
				close(teamRunners)
			}()

			// everything spawned, so wait until they all finish
			teamRoundData := []checks.Result{} // slice per goroutine for safer concurrency
			waitForTeam := true
			for waitForTeam {
				select {
				case result, ok := <-teamRunners:
					if !ok {
						errorPrint("Service check result for", team.Name, result.ServiceName, "received after channel closed")
						return // break? should this fail?
					}
					teamRoundData = append(teamRoundData, result)
					thisTeamWg.Done()
				case <-teamCompleteChan:
					close(teamCompleteChan)
					roundDataMutex.Lock()
					roundData[team.ID] = teamRoundData
					roundDataMutex.Unlock()
					waitForTeam = false
					debugPrint("[SCORE] Checks for team", team.Name, "are done!")
				}
			}

			// all checks for this team are done, so decrement waitgroup
			allTeamsWg.Done()
		}(team)
	}
	allTeamsWg.Wait()
	return roundData
}

// needs to be rewritten
func detectSLAs(m Config, roundData map[uint][]checks.Result) error {
	teams, err := dbGetTeams()
	if err != nil {
		return err
	}
	for _, box := range m.Box {
		for _, runner := range box.Runners {
			if roundStartTime.Before(runner.GetService().LaunchTime) {
				continue
			}
			for _, team := range teams {
				var result checks.Result
				for _, checkResult := range roundData[team.ID] {
					if checkResult.ServiceName == runner.GetService().Name {
						result = checkResult
						break
					}
				}
				checkUptime, exists := uptime[team.ID][runner.GetService().Name]
				if !exists {
					uptime[team.ID][runner.GetService().Name] = Uptime{}
				}
				checkUptime.Total++
				if result.Status == false {
					if _, exists := slaCount[team.ID]; !exists {
						slaCount[team.ID] = make(map[string]int)
					}
					slaCount[team.ID][runner.GetService().Name]++
					if slaCount[team.ID][runner.GetService().Name] == runner.GetService().SlaThreshold {
						err = dbCreateSLA(team.ID, runner.GetService().Name, roundNumber, runner.GetService().SlaPenalty)
						if err != nil {
							errorPrint("Failed to create SLA for", team.Name, runner.GetService().Name)
						}
						slaCount[team.ID][runner.GetService().Name] = 0
					}
				} else {
					checkUptime.Ups++
				}
				uptime[team.ID][runner.GetService().Name] = checkUptime
			}
		}
	}
	return nil
}
