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
	"strings"
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

// commandCredentials reports the credlists a command reads through PASSWORD[name], whether it also
// uses USERNAME or a bare PASSWORD, and whether it has a PASSWORD[ that is not a valid named placeholder.
func commandCredentials(command string) (named []string, plain bool, malformed bool) {
	matched := 0
	for _, m := range commandPlaceholder.FindAllStringSubmatch(command, -1) {
		switch {
		case m[1] != "":
			matched++
			if !slices.Contains(named, m[1]) {
				named = append(named, m[1])
			}
		case m[0] == "USERNAME" || m[0] == "PASSWORD":
			plain = true
		}
	}
	return named, plain, strings.Count(command, "PASSWORD[") != matched
}

// credentials draws the login for USERNAME and PASSWORD (only when the command uses them) from the
// credlists it does not name, and returns the shell-escaped password of each credlist it does name.
func (c *Custom) credentials(teamID uint, named []string, plain bool) (username, password string, secrets map[string]string, err error) {
	if len(c.CredLists) > 0 && plain {
		if username, password, err = c.getCreds(teamID, named...); err != nil {
			return "", "", nil, err
		}
	}
	secrets = make(map[string]string, len(named))
	for _, name := range named {
		value, err := c.getCredlistPassword(name)
		if err != nil {
			return "", "", nil, err
		}
		secrets[name] = shellescape.Quote(value)
	}
	return username, password, secrets, nil
}

func (c Custom) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		named, plain, _ := commandCredentials(c.Command)
		username, password, secrets, err := c.credentials(teamID, named, plain)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// Replace command input keywords. Credentials are shell escaped, who knows what format they are.
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
			return secrets[match[len("PASSWORD["):len(match)-1]]
		})
		slog.Debug("CUSTOM CHECK COMMAND", "command", formedCommand)
		checkResult.Debug = formedCommand

		// Create command with timeout context
		timeout := time.Duration(c.Timeout) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", formedCommand) // #nosec G204 -- custom checks intentionally run user-defined commands
		cmd.Env = append(os.Environ(), ChromeEnv()...)

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
	named, plain, malformed := commandCredentials(c.Command)
	if malformed {
		return fmt.Errorf("custom check %s has a PASSWORD[ that is not PASSWORD[credlist] with a name free of spaces and ]", c.Name)
	}
	for _, name := range named {
		if !slices.Contains(c.CredLists, name) {
			return fmt.Errorf("custom check %s reads PASSWORD[%s], which is not one of its credlists", c.Name, name)
		}
	}
	allReadByName := !slices.ContainsFunc(c.CredLists, func(list string) bool { return !slices.Contains(named, list) })
	if plain && len(c.CredLists) > 0 && allReadByName {
		return fmt.Errorf("custom check %s uses USERNAME or PASSWORD but reads every one of its credlists by name", c.Name)
	}

	return nil
}

// ChromeEnv is the environment that tells custom checks (browser-curl.sh)
// which Chrome to claim, so ChromeMajor stays the only place it is set.
func ChromeEnv() []string {
	return []string{
		"QUOTIENT_CHROME_MAJOR=" + strconv.Itoa(ChromeMajor),
		"QUOTIENT_CHROME_SEC_CH_UA=" + ChromeSecCHUA(ChromeMajor),
	}
}
