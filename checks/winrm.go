package checks

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/masterzen/winrm"
)

type WinRM struct {
	Service
	Encrypted   bool
	BadAttempts int
	Command     []winCommandData
}

type winCommandData struct {
	UseRegex bool `toml:",omitempty"`
	Contains bool `toml:",omitempty"`
	Command  string
	Output   string
}

func (c WinRM) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	username, password := getCreds(teamID, c.CredLists)
	params := *winrm.DefaultParameters

	// Run bad attempts if specified
	for i := 0; i < c.BadAttempts; i++ {
		endpoint := winrm.NewEndpoint(target, c.Port, c.Encrypted, true, nil, nil, nil, time.Duration(c.Timeout)*time.Second)
		winrm.NewClientWithParameters(endpoint, username, uuid.New().String(), &params)
	}

	// Log in to WinRM
	endpoint := winrm.NewEndpoint(target, c.Port, c.Encrypted, true, nil, nil, nil, time.Duration(c.Timeout)*time.Second)
	client, err := winrm.NewClientWithParameters(endpoint, username, password, &params)
	if err != nil {
		res <- Result{
			Error: "error creating winrm client",
			Debug: err.Error(),
		}
	}

	// If any commands specified, run them
	if len(c.Command) > 0 {
		r := c.Command[rand.Intn(len(c.Command))]
		powershellCmd := winrm.Powershell(r.Command)
		bufOut := new(bytes.Buffer)
		bufErr := new(bytes.Buffer)
		_, err = client.Run(powershellCmd, bufOut, bufErr)
		output := bufOut.Bytes()
		errString := bufErr.String()
		if err != nil {
			res <- Result{
				Error: "failed with creds " + username + ":" + password,
				Debug: err.Error(),
			}
			return
		} else if errString != "" {
			res <- Result{
				Error: "command produced an error message",
				Debug: "error: " + errString,
			}
			return
		}
		if r.Output != "" {
			if r.Contains {
				if r.UseRegex {
					re := regexp.MustCompile(r.Output)
					found := re.Find(output)
					if len(found) == 0 {
						res <- Result{
							Error: "command output didn't contain regex",
							Debug: "command output of '" + r.Command + "' didn't contain regex '" + r.Output,
						}
						return
					}
				} else {
					if !strings.Contains(string(output), r.Output) {
						res <- Result{
							Error: "command output didn't contain string",
							Debug: "command output of '" + r.Command + "' didn't contain string '" + r.Output,
						}
						return
					}
				}
			} else {
				if r.UseRegex {
					re := regexp.MustCompile(r.Output)
					if !re.Match(output) {
						res <- Result{
							Error: "command output didn't match regex",
							Debug: "command output'" + r.Command + "' didn't match regex '" + r.Output,
						}
						return
					}
				} else {
					if strings.TrimSpace(string(output)) != r.Output {
						res <- Result{
							Error: "command output didn't match string",
							Debug: "command output of '" + r.Command + "' didn't match string '" + r.Output,
						}
						return
					}
				}
			}
		}
	}
	res <- Result{
		Status: true,
		Points: c.Points,
		Debug:  "creds used were " + username + ":" + password,
	}
}

func (c WinRM) GetService() Service {
	return c.Service
}
