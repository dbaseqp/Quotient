package checks

import (
	"io"
	"math/rand"
	"regexp"
	"strconv"
	"time"

	"github.com/jlaffaye/ftp"
)

type Ftp struct {
	Service
	File []FtpFile
}

type FtpFile struct {
	Name  string
	Hash  string
	Regex string
}

func (c Ftp) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	conn, err := ftp.Dial(target+":"+strconv.Itoa(c.Port), ftp.DialWithTimeout(time.Duration(c.Timeout)*time.Second))
	if err != nil {
		res <- Result{
			Error: "ftp connection failed",
			Debug: err.Error(),
		}
		return
	}
	defer conn.Quit()

	var username, password string
	if c.Anonymous {
		username = "anonymous"
		password = "anonymous"
	} else {
		username, password = getCreds(teamID, c.CredLists)
	}
	err = conn.Login(username, password)
	if err != nil {
		res <- Result{
			Error: "ftp login failed",
			Debug: "creds used were " + username + ":" + password + " with error " + err.Error(),
		}
		return
	}

	if len(c.File) > 0 {
		file := c.File[rand.Intn(len(c.File))]
		r, err := conn.Retr(file.Name)
		if err != nil {
			res <- Result{
				Error: "failed to retrieve file " + file.Name,
				Debug: "creds used were " + username + ":" + password,
			}
			return
		}
		defer r.Close()
		buf, err := io.ReadAll(r)
		if err != nil {
			res <- Result{
				Error: "failed to read ftp file",
				Debug: "tried to read " + file.Name,
			}
			return
		}
		if file.Regex != "" {
			re, err := regexp.Compile(file.Regex)
			if err != nil {
				res <- Result{
					Error: "error compiling regex to match for ftp file",
					Debug: err.Error(),
				}
				return
			}
			reFind := re.Find(buf)
			if reFind == nil {
				res <- Result{
					Error: "couldn't find regex in file",
					Debug: "couldn't find regex \"" + file.Regex + "\" for " + file.Name,
				}
				return
			}
		} else if file.Hash != "" {
			fileHash, err := StringHash(string(buf))
			if err != nil {
				res <- Result{
					Error: "error calculating file hash",
					Debug: err.Error(),
				}
				return
			} else if fileHash != file.Hash {
				res <- Result{
					Error: "file hash did not match",
					Debug: "file hash " + fileHash + " did not match specified hash " + file.Hash,
				}
				return
			}
		}
	}

	res <- Result{
		Status: true,
		Points: c.Points,
		Debug:  "creds used were " + username + ":" + password,
	}
}

func (c Ftp) GetService() Service {
	return c.Service
}
