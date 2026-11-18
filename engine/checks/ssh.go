package checks

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

type Ssh struct {
	Service
	Domain      string `toml:",omitempty"`
	PrivKey     string `toml:",omitempty"`
	BadAttempts int    `toml:",omitzero"`
	Command     []commandData
}

type commandData struct {
	UseRegex bool
	Contains bool
	Command  string `toml:",omitempty"`
	Output   string `toml:",omitempty"`
}

func formatSSHUsername(username, domain, teamIdentifier string) string {
	d := strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(domain, "_", teamIdentifier)), "@")
	if d == "" || strings.ContainsAny(username, `@\`) {
		return username
	}
	return username + "@" + d
}

func (c Ssh) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {

		// Create client config
		username, password, err := c.getCreds(teamID)
		if err != nil {
			checkResult.Error = "error getting creds"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		username = formatSSHUsername(username, c.Domain, teamIdentifier)

		timeout := time.Duration(c.Timeout) * time.Second
		addr := c.Target + ":" + strconv.Itoa(c.Port)
		profile := sshProfileFor(c.Target)
		var auth []ssh.AuthMethod
		if c.PrivKey != "" {
			key, err := os.ReadFile("./config/scoredfiles/" + c.PrivKey)
			if err != nil {
				checkResult.Error = "error opening pubkey"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				checkResult.Error = "error parsing private key"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			auth = []ssh.AuthMethod{
				ssh.PublicKeys(signer),
			}
		} else {
			auth = []ssh.AuthMethod{
				ssh.Password(password),
			}
		}

		for range c.BadAttempts {
			badAuth := []ssh.AuthMethod{ssh.Password(uuid.New().String())}
			badConn, err := profile.dial(addr, username, badAuth, timeout, false)
			if err == nil {
				if err := badConn.Close(); err != nil {
					slog.Error("failed to close bad ssh connection", "error", err)
				}
			}
		}

		// Connect to ssh server
		conn, err := profile.dial(addr, username, auth, timeout, true)
		if err != nil {
			if c.PrivKey != "" {
				checkResult.Error = "error logging in to ssh server with private key " + c.PrivKey
				checkResult.Debug = "error: " + err.Error()
			} else {
				checkResult.Error = "error logging in to ssh server for creds " + username + ":" + password
				checkResult.Debug = "error: " + err.Error()
			}
			response <- checkResult
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				slog.Error("failed to close ssh connection", "error", err)
			}
		}()

		// Create a session
		session, err := conn.NewSession()
		if err != nil {
			checkResult.Error = "unable to create ssh session"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}
		// nolint:errcheck
		defer session.Close()
		// Like OpenSSH, promise the server no further sessions on this connection.
		_, _, _ = conn.SendRequest("no-more-sessions@openssh.com", false, nil)

		// Request pseudo terminal
		if err := requestOpenSSHPty(session); err != nil {
			checkResult.Error = "couldn't allocate pts"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		// I/O for shell
		stdin, err := session.StdinPipe()
		if err != nil {
			checkResult.Error = "couldn't get stdin pipe"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		var stdoutBytes bytes.Buffer
		var stderrBytes bytes.Buffer
		session.Stdout = &stdoutBytes
		session.Stderr = &stderrBytes

		// Start remote shell
		if err := session.Shell(); err != nil {
			checkResult.Error = "failed to start shell"
			checkResult.Debug = "error: " + err.Error()
			response <- checkResult
			return
		}

		// If any commands specified, run a random one
		if len(c.Command) > 0 {
			r := c.Command[rand.Intn(len(c.Command))] // #nosec G404 -- non-crypto selection of command to test
			_, _ = fmt.Fprintln(stdin, r.Command)
			time.Sleep(time.Duration(int(time.Duration(c.Timeout)*time.Second) / 8)) // command wait time
			if r.Contains {
				if !strings.Contains(stdoutBytes.String(), r.Output) {
					checkResult.Error = "command output didn't contain string"
					checkResult.Debug = "command output of '" + r.Command + "' didn't contain string '" + r.Output + "': " + stdoutBytes.String() + ",  " + stderrBytes.String()
					response <- checkResult
					return
				}
			} else if r.UseRegex {
				re := regexp.MustCompile(r.Output)
				if !re.Match(stdoutBytes.Bytes()) {
					checkResult.Error = "command output didn't match regex"
					checkResult.Debug = "command output'" + r.Command + "' didn't match regex '" + r.Output
					response <- checkResult
					return
				}
			} else {
				if stderrBytes.Len() != 0 {
					checkResult.Error = "command returned an error"
					checkResult.Debug = "command stderr was not empty: " + stderrBytes.String()
					response <- checkResult
					return
				}
			}
		}
		checkResult.Status = true
		checkResult.Debug = "creds used were " + username + ":" + password
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Ssh) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Ssh"
	}
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Display == "" {
		c.Display = "ssh"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.PrivKey != "" && c.BadAttempts != 0 {
		return errors.New("cannot use both private key and bad attempts")
	}
	for _, r := range c.Command {
		if r.UseRegex {
			regexp.MustCompile(r.Output)
		}
		if r.UseRegex {
			regexp.MustCompile(r.Output)
		}
	}

	return nil
}
