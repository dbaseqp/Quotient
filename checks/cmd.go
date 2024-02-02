package checks

import (
	"os/exec"
	"regexp"
	"strings"

	"github.com/alessio/shellescape"
)

type Cmd struct {
	Service
	Command string
	Regex   string
}

func commandOutput(cmd string) (string, error) {

	out, err := exec.Command("/bin/sh", "-c", cmd).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c Cmd) Run(teamID uint, boxIp string, res chan Result, service Service) {
	re, err := regexp.Compile(c.Regex)
	if err != nil {
		res <- Result{
			Error: "error compiling regex",
			Debug: err.Error(),
		}
		return
	}

	username, password := getCreds(teamID, service.CredLists)

	// Replace command input keywords
	formedCommand := strings.Replace(c.Command, "BOXIP", boxIp, -1)

	// We shell escape username and password, who knows what format they are
	formedCommand = strings.Replace(formedCommand, "USERNAME", shellescape.Quote(username), -1)
	formedCommand = strings.Replace(formedCommand, "PASSWORD", shellescape.Quote(password), -1)

	out, err := commandOutput(formedCommand)
	if err != nil {
		res <- Result{
			Error: "command returned error",
			Debug: err.Error(),
		}
		return
	}

	reFind := re.Find([]byte(out))
	if reFind == nil {
		res <- Result{
			Error: "output incorrect",
			Debug: "couldn't find regex \"" + c.Regex + "\" in " + out,
		}
		return
	}

	res <- Result{
		Status: true,
		Debug:  out,
	}
}
