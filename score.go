package main

import (
	"log"
	"math/rand"
	"sugmaase/checks"
	"sync"
	"time"
)

func Score() {
	// wait until engine is not paused
	for {
		log.Println("[SCORE] ===== Queuing round", roundNumber)
		enginePauseWg.Wait()

		// run this round
		log.Println("[SCORE] ===== Starting round", roundNumber)
		startTime := time.Now()

		jitter := time.Duration(0)
		if eventConf.Jitter != 0 {
			jitter = time.Duration(time.Duration(rand.Intn(eventConf.Jitter+1)) * time.Second)
		}
		nextRoundTime := startTime.Add((time.Duration(eventConf.Delay) * time.Second) + jitter)
		log.Println("[SCORE] ===== Next round scheduled after", eventConf.Delay, "seconds with jitter", jitter)

		// pass current state of eventConf so it is stable for duration of this round
		var roundData map[uint][]checks.Result
		switch eventConf.EventType {
		case "rvb":
			roundData = scoreRvB(eventConf)
		case "koth":
			roundData = scoreKoTH(eventConf)
		}
		err := dbCreateRound(roundNumber, startTime)
		if err != nil {
			errorPrint(err.Error())
		}
		err = dbProcessRound(roundData)
		if err != nil {
			// if you see this, that is very bad
			errorPrint("FAILED TO SAVE ROUND DATA FOR ROUND", roundNumber, ":", err.Error())
		}
		// these may be RvB only functions that need to be refactored
		err = dbUpdateCumulativeServiceScoreCache(roundData)
		if err != nil {
			errorPrint("FAILED TO UPDATE CUMULATIVE SCORE CACHE DATA FOR ROUND", roundNumber, ":", err.Error())
		}

		err = detectSLAs(roundData)
		if err != nil {
			errorPrint("FAILED TO GENERATE SLA DATA FOR ROUND", roundNumber, ":", err.Error())
		}
		err = makeGraphs()
		if err != nil {
			errorPrint("FAILED TO MAKE GRAPHS FOR ROUND", roundNumber, ":", err.Error())
		}
		log.Println("[SCORE] ===== Ending for round", roundNumber)
		debugPrint("Round", roundNumber, "took", time.Now().Sub(startTime).String(), "to finish")

		// prepare for next round
		sleepDuration := nextRoundTime.Sub(time.Now())
		log.Println("[SCORE] Sleeping for", sleepDuration, "seconds until next round")
		time.Sleep(sleepDuration)
		roundNumber++
	}
}

func scoreKoTH(m Config) map[uint][]checks.Result {
	// TODO
	// roundData := make(map[uint][]checks.Result)

	time.Sleep(5 * time.Second)
	return nil
}

func scoreRvB(m Config) map[uint][]checks.Result {
	roundData := make(map[uint][]checks.Result)

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
			resultsChan := make(chan checks.Result) // channel to handle checks finishing
			thisTeamWg := &sync.WaitGroup{}
			// spawn all the checks for this team then wait to process once all checks finish

			for _, box := range m.Box {
				for _, check := range box.CheckList {
					if time.Now().Before(check.LaunchTime) || time.Now().After(check.StopTime) {
						continue
					}
					thisTeamWg.Add(1)
					debugPrint("[SCORE] Running", check.Name, "for", team.Name)
					// go check.Run(team.ID, box.IP, resultsChan)
					timeout := m.Timeout
					// if check.Timeout {
					// create per check timeout handling
					// }
					go checks.RunCheck(team.ID, team.IP, box.IP, box.Name, check, time.Duration(timeout)*time.Second, resultsChan)
				}
			}

			// goroutine to detect team completion. located after checks have been spawned so channel doesn't close early
			teamCompleteChan := make(chan bool) // channel to handle this team finishing
			go func() {
				thisTeamWg.Wait()
				teamCompleteChan <- true
				close(resultsChan)
			}()

			// everything spawned, so wait until they all finish
			teamRoundData := []checks.Result{} // slice per goroutine for safer concurrency
			waitForTeam := true
			for waitForTeam {
				select {
				case result, ok := <-resultsChan:
					if !ok {
						errorPrint("Service check result for", team.Name, result.Name, "received after channel closed")
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

func detectSLAs(roundData map[uint][]checks.Result) error {
	teams, err := dbGetTeams()
	if err != nil {
		return err
	}
	for _, box := range eventConf.Box {
		for _, check := range box.CheckList {
			for _, team := range teams {
				var result checks.Result
				for _, checkResult := range roundData[team.ID] {
					if checkResult.Name == check.Name {
						result = checkResult
						break
					}
				}
				if result.Status == false {
					if _, exists := slaCount[team.ID]; !exists {
						slaCount[team.ID] = make(map[string]int)
					}
					slaCount[team.ID][check.Name]++
					if slaCount[team.ID][check.Name] == check.SlaThreshold {
						err = dbCreateSLA(team.ID, check.Name, roundNumber, check.SlaPenalty)
						if err != nil {
							errorPrint("Failed to create SLA for", team.Name, check.Name)
						}
						slaCount[team.ID][check.Name] = 0
					}
				}
			}
		}
	}
	return nil
}
