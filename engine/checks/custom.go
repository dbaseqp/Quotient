package checks

import (
	"errors"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

type Custom struct {
	Service
	Command string
	Regex   string
}

func commandOutput(cmd string) (string, error) {

	out, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c Custom) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		var username, password string
		var err error
		if len(c.CredLists) > 0 {
			username, password, err = c.getCreds(teamID)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
		}

		// Replace command input keywords
		formedCommand := c.Command
		formedCommand = strings.Replace(formedCommand, "ROUND", strconv.FormatUint(uint64(roundID), 10), -1)
		formedCommand = strings.Replace(formedCommand, "TARGET", c.Target, -1) // is there a case where u need IP and FQDN?
		formedCommand = strings.Replace(formedCommand, "TEAMIDENTIFIER", teamIdentifier, -1)

		// We shell escape username and password, who knows what format they are
		formedCommand = strings.Replace(formedCommand, "USERNAME", shellescape.Quote(username), -1)
		formedCommand = strings.Replace(formedCommand, "PASSWORD", shellescape.Quote(password), -1)
		slog.Debug("CUSTOM CHECK COMMAND", "command", formedCommand)
		checkResult.Debug = formedCommand
		out, err := commandOutput(formedCommand)
		if err != nil {
			checkResult.Error = "command returned error" + out
			checkResult.Debug += " " + err.Error()
			response <- checkResult
			return
		}
		if c.Regex != "" {
			re, err := regexp.Compile(c.Regex)
			if err != nil {
				checkResult.Error = "error compiling regex"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}

			reFind := re.Find([]byte(out))
			if reFind == nil {
				checkResult.Error = "output incorrect"
				checkResult.Debug += " couldn't find regex \"" + c.Regex + "\" in " + out
				response <- checkResult
				return
			}
			checkResult.Status = true
			checkResult.Debug += " found regex \"" + c.Regex + "\" in " + out
			response <- checkResult
			return
		}

		checkResult.Status = true
		checkResult.Debug += " " + out
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Custom) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Custom"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "custom"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Command == "" {
		return errors.New("no command found for custom check " + c.Name)
	}

	return nil
}
