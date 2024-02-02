package checks

import (
	"bytes"
	"fmt"
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
	PrivKey     string
	BadAttempts int
	Command     []commandData
}

type commandData struct {
	UseRegex bool
	Contains bool
	Command  string
	Output   string
}

func (c Ssh) Run(teamID uint, boxIp string, res chan Result, service Service) {
	// Create client config
	username, password := getCreds(teamID, service.CredLists)
	config := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         GlobalTimeout,
	}
	config.SetDefaults()
	config.Ciphers = append(config.Ciphers, "3des-cbc")
	if c.PrivKey != "" {
		key, err := os.ReadFile("./scoredfiles/" + c.PrivKey)
		if err != nil {
			res <- Result{
				Error: "error opening pubkey",
				Debug: err.Error(),
			}
			return
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			res <- Result{
				Error: "error parsing private key",
				Debug: err.Error(),
			}
			return
		}
		config.Auth = []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		}
	} else {
		config.Auth = []ssh.AuthMethod{
			ssh.Password(password),
		}
	}

	for i := 0; i < c.BadAttempts; i++ {
		badConf := &ssh.ClientConfig{
			User: username,
			Auth: []ssh.AuthMethod{
				ssh.Password(uuid.New().String()),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         GlobalTimeout,
		}

		badConn, err := ssh.Dial("tcp", boxIp+":"+strconv.Itoa(service.Port), badConf)
		if err == nil {
			badConn.Close()
		}
	}

	// Connect to ssh server
	conn, err := ssh.Dial("tcp", boxIp+":"+strconv.Itoa(service.Port), config)
	if err != nil {
		if c.PrivKey != "" {
			res <- Result{
				Error: "error logging in to ssh server with private key " + c.PrivKey,
				Debug: "error: " + err.Error(),
			}
		} else {
			res <- Result{
				Error: "error logging in to ssh server for creds " + username + ":" + password,
				Debug: "error: " + err.Error(),
			}
		}
		return
	}
	defer conn.Close()

	// Create a session
	session, err := conn.NewSession()
	if err != nil {
		res <- Result{
			Error: "unable to create ssh session",
			Debug: err.Error(),
		}
		return
	}
	defer session.Close()

	// Set up terminal modes
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	// Request pseudo terminal
	if err := session.RequestPty("xterm", 40, 80, modes); err != nil {
		res <- Result{
			Error: "couldn't allocate pts",
			Debug: err.Error(),
		}
		return
	}

	// I/O for shell
	stdin, err := session.StdinPipe()
	if err != nil {
		res <- Result{
			Error: "couldn't get stdin pipe",
			Debug: err.Error(),
		}
		return
	}

	var stdoutBytes bytes.Buffer
	var stderrBytes bytes.Buffer
	session.Stdout = &stdoutBytes
	session.Stderr = &stderrBytes

	// Start remote shell
	if err := session.Shell(); err != nil {
		res <- Result{
			Error: "failed to start shell",
			Debug: "error: " + err.Error(),
		}
		return
	}

	// If any commands specified, run a random one
	if len(c.Command) > 0 {
		r := c.Command[rand.Intn(len(c.Command))]
		fmt.Fprintln(stdin, r.Command)
		time.Sleep(time.Duration(int(GlobalTimeout) / 8))
		if r.Contains {
			if !strings.Contains(stdoutBytes.String(), r.Output) {
				res <- Result{
					Error: "command output didn't contain string",
					Debug: "command output of '" + r.Command + "' didn't contain string '" + r.Output + "': " + stdoutBytes.String() + ",  " + stderrBytes.String(),
				}
				return
			}
		} else if r.UseRegex {
			re := regexp.MustCompile(r.Output)
			if !re.Match([]byte(stdoutBytes.String())) {
				res <- Result{
					Error: "command output didn't match regex",
					Debug: "command output'" + r.Command + "' didn't match regex '" + r.Output,
				}
				return
			} else {
				if strings.TrimSpace(stdoutBytes.String()) != r.Output {
					res <- Result{
						Error: "command output didn't match string",
						Debug: "command output of '" + r.Command + "' didn't match string '" + r.Output,
					}
					return
				}
			}
		} else {
			if stderrBytes.Len() != 0 {
				res <- Result{
					Error: "command returned an error",
					Debug: "command stderr was not empty: " + stderrBytes.String(),
				}
				return
			}
		}
	}
	res <- Result{
		Status: true,
		Debug:  "creds used were " + username + ":" + password,
	}
}
