package checks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
)

type Custom struct {
	Service
	Command string
	Regex   string
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

		// Create command with timeout context
		timeout := time.Duration(c.Timeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", formedCommand) // #nosec G204 -- custom checks intentionally run user-defined commands

		// Use os.Root to safely handle temp file operations
		tmpRoot, err := os.OpenRoot("/tmp")
		if err != nil {
			checkResult.Error = "error opening tmp directory"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
			if err := tmpRoot.Close(); err != nil {
				slog.Error("failed to close tmp root directory", "error", err)
			}
		}()

		tmpfileName := fmt.Sprintf("custom-check-%d-%d-%s", roundID, teamID, c.Name)
		tmpfile, err := tmpRoot.Create(tmpfileName)
		if err != nil {
			checkResult.Error = "error creating tmpfile"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		defer func() {
			if err := tmpfile.Close(); err != nil {
				slog.Error("failed to close custom check tmpfile", "error", err)
			}
			if err := tmpRoot.Remove(tmpfileName); err != nil {
				slog.Error("failed to remove custom check tmpfile", "error", err)
			}
		}()

		cmd.Stdout = tmpfile
		cmd.Stderr = tmpfile

		err = cmd.Run()

		// Read back the temp file using the root
		tmpfileRead, err2 := tmpRoot.Open(tmpfileName)
		if err2 != nil {
			checkResult.Debug += fmt.Sprintf("\nerror opening tmpfile for reading:\n%s", err2.Error())
			response <- checkResult
			return
		}
		defer func() {
			if err := tmpfileRead.Close(); err != nil {
				slog.Error("failed to close custom check tmpfile after reading", "error", err)
			}
		}()

		raw, err2 := io.ReadAll(tmpfileRead)
		if err2 != nil {
			checkResult.Debug += fmt.Sprintf("\nerror reading tmpfile:\n%s", err2.Error())
			response <- checkResult
			return
		}
		out := string(raw)
		if err != nil {
			checkResult.Error += fmt.Sprintf("command returned error:\n%s", err.Error())
			checkResult.Debug += fmt.Sprintf("\noutput:\n%s", out)
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
