package checks

import (
	"crypto/tls"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

type Web struct {
	Service
	Url    []urlData
	Scheme string
}

type urlData struct {
	Path        string
	Status      int    `toml:",omitempty"`
	Diff        int    `toml:",omitempty"`
	Regex       string `toml:",omitempty"`
	CompareFile string `toml:",omitempty"` // TODO implement
}

func (c Web) Run(teamID uint, teamIdentifier string, target string, res chan Result) {
	u := c.Url[rand.Intn(len(c.Url))]

	tr := &http.Transport{
		MaxIdleConns:      1,
		IdleConnTimeout:   time.Duration(c.Timeout) * time.Second, // address this
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Get(c.Scheme + "://" + target + ":" + strconv.Itoa(c.Port) + u.Path)
	if err != nil {
		res <- Result{
			Error: "web request errored out",
			Debug: err.Error() + " for url " + u.Path,
		}
		return
	}

	if u.Status != 0 && resp.StatusCode != u.Status {
		res <- Result{
			Error: "status returned by webserver was incorrect",
			Debug: "status was " + strconv.Itoa(resp.StatusCode) + " wanted " + strconv.Itoa(u.Status) + " for url " + u.Path,
		}
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		res <- Result{
			Error: "error reading page content",
			Debug: "error was '" + err.Error() + "' for url " + u.Path,
		}
		return
	}

	if u.Regex != "" {
		re, err := regexp.Compile(u.Regex)
		if err != nil {
			res <- Result{
				Error: "error compiling regex to match for web page",
				Debug: err.Error(),
			}
			return
		}
		reFind := re.Find(body)
		if reFind == nil {
			res <- Result{
				Error: "didn't find regex on page",
				Debug: "couldn't find regex \"" + u.Regex + "\" for " + u.Path,
			}
			return
		} else {
			res <- Result{
				Status: true,
				Points: c.Points,
				Debug:  "matched regex \"" + u.Regex + "\" for " + u.Path,
			}
			return
		}

	}

	res <- Result{
		Status: true,
		Points: c.Points,
	}
}

func (c Web) GetService() Service {
	return c.Service
}
