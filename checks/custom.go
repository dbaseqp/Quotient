package checks

import (
	"log"
	"os/exec"
	"regexp"
	"strings"

	"github.com/alessio/shellescape"
)

type Custom struct {
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

func (c Custom) Run(teamID uint, boxIp string, boxFQDN string, res chan Result) {
	re, err := regexp.Compile(c.Regex)
	if err != nil {
		res <- Result{
			Error: "error compiling regex",
			Debug: err.Error(),
		}
		return
	}

	username, password := getCreds(teamID, c.CredLists)

	// Replace command input keywords
	formedCommand := c.Command
	formedCommand = strings.Replace(formedCommand, "BOXIP", boxIp, -1)
	formedCommand = strings.Replace(formedCommand, "FQDN", boxFQDN, -1)

	// We shell escape username and password, who knows what format they are
	formedCommand = strings.Replace(formedCommand, "USERNAME", shellescape.Quote(username), -1)
	formedCommand = strings.Replace(formedCommand, "PASSWORD", shellescape.Quote(password), -1)
	log.Println("CUSTOM CHECK COMMAND:", formedCommand)
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
		Points: c.Points,
		Debug:  out,
	}
}

func (c Custom) GetService() Service {
	return c.Service
}
