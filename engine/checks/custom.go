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
	"slices"
	"strconv"
	"time"

	"al.essio.dev/pkg/shellescape"
)

type Custom struct {
	Service
	Command string
	Regex   string
}

// commandPlaceholder matches every keyword a command may contain. PASSWORD[name] is listed before
// PASSWORD so it wins at the same position. The command is rewritten in a single pass, so a value
// inserted for one keyword (a team controls its passwords) is never scanned for the others.
var commandPlaceholder = regexp.MustCompile(`PASSWORD\[([^\]\s]+)\]|ROUND|TARGET|TEAMIDENTIFIER|USERNAME|PASSWORD`)

// namedCredlists returns the credlists a command reads through PASSWORD[name].
func namedCredlists(command string) []string {
	var names []string
	for _, m := range commandPlaceholder.FindAllStringSubmatch(command, -1) {
		if m[1] != "" && !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return names
}

// usesPlainCredentials reports whether a command contains USERNAME or a bare PASSWORD.
func usesPlainCredentials(command string) bool {
	for _, m := range commandPlaceholder.FindAllStringSubmatch(command, -1) {
		if m[0] == "USERNAME" || m[0] == "PASSWORD" {
			return true
		}
	}
	return false
}

func (c Custom) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		// USERNAME and PASSWORD come from the credlists the command does not name; a named credlist
		// is only ever read through its own PASSWORD[name].
		var username, password string
		var err error
		if len(c.CredLists) > 0 && usesPlainCredentials(c.Command) {
			username, password, err = c.getCreds(teamID, namedCredlists(c.Command)...)
			if err != nil {
				checkResult.Error = "error getting creds"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
		}

		// Replace command input keywords. Credentials are shell escaped, who knows what format they are.
		named := map[string]string{}
		var namedErr error
		formedCommand := commandPlaceholder.ReplaceAllStringFunc(c.Command, func(match string) string {
			switch match {
			case "ROUND":
				return strconv.FormatUint(uint64(roundID), 10)
			case "TARGET":
				return c.Target // is there a case where u need IP and FQDN?
			case "TEAMIDENTIFIER":
				return teamIdentifier
			case "USERNAME":
				return shellescape.Quote(username)
			case "PASSWORD":
				return shellescape.Quote(password)
			}
			name := match[len("PASSWORD[") : len(match)-1]
			if _, ok := named[name]; !ok {
				value, err := c.getCredlistPassword(name)
				if err != nil {
					namedErr = err
				}
				named[name] = shellescape.Quote(value)
			}
			return named[name]
		})
		if namedErr != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = namedErr.Error()
			response <- checkResult
			return
		}
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

		tmpfileName := fmt.Sprintf("custom-check-%d-%d-%s-%d", roundID, teamID, c.Name, time.Now().UnixNano())
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
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
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
	for _, name := range namedCredlists(c.Command) {
		if !slices.Contains(c.CredLists, name) {
			return fmt.Errorf("custom check %s reads PASSWORD[%s], which is not one of its credlists", c.Name, name)
		}
	}

	return nil
}
