package checks

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/corpix/uarand"
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

func (c Web) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		u := c.Url[rand.Intn(len(c.Url))]

		// random user agent
		ua := uarand.GetRandom()

		tr := &http.Transport{
			MaxIdleConns:      1,
			IdleConnTimeout:   time.Duration(c.Timeout) * time.Second, // address this
			DisableKeepAlives: true,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
		// Set client timeout to slightly less than check timeout to get better error messages
		clientTimeout := time.Duration(c.Timeout-1) * time.Second
		client := &http.Client{
			Transport: tr,
			Timeout:   clientTimeout,
		}

		requestURL := fmt.Sprintf("%s://%s:%d%s", c.Scheme, c.Target, c.Port, u.Path)
		req, err := http.NewRequest("GET", requestURL, nil)
		if err != nil {
			checkResult.Error = "error creating web request"
			checkResult.Debug = err.Error()
			response <- checkResult
			return
		}

		req.Header.Set("User-Agent", ua)

		// Store request info for timeout debugging
		checkResult.Debug = fmt.Sprintf("Attempting GET %s", requestURL)

		resp, err := client.Do(req)
		if err != nil {
			checkResult.Error = "web request errored out"
			if strings.Contains(err.Error(), "Client.Timeout exceeded") {
				checkResult.Debug = fmt.Sprintf("HTTP request to %s timed out after %v (TCP connection may have succeeded but server did not respond)", requestURL, clientTimeout)
			} else {
				checkResult.Debug = err.Error() + " for url " + u.Path
			}
			response <- checkResult
			return
		}

		if u.Status != 0 && resp.StatusCode != u.Status {
			checkResult.Error = "status returned by webserver was incorrect"
			checkResult.Debug = "status was " + strconv.Itoa(resp.StatusCode) + " wanted " + strconv.Itoa(u.Status) + " for url " + u.Path
			response <- checkResult
			return
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			checkResult.Error = "error reading page content"
			checkResult.Debug = "error was '" + err.Error() + "' for url " + u.Path
			response <- checkResult
			return
		}

		if u.Regex != "" {
			re, err := regexp.Compile(u.Regex)
			if err != nil {
				checkResult.Error = "error compiling regex to match for web page"
				checkResult.Debug = err.Error()
				response <- checkResult
				return
			}
			reFind := re.Find(body)
			if reFind == nil {
				checkResult.Error = "didn't find regex on page"
				checkResult.Debug = "couldn't find regex \"" + u.Regex + "\" for " + u.Path
				response <- checkResult
				return
			} else {
				checkResult.Status = true
				checkResult.Debug = "matched regex \"" + u.Regex + "\" for " + u.Path
				response <- checkResult
				return
			}
		}

		checkResult.Status = true
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

func (c *Web) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Web"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}

	if c.Scheme == "" {
		c.Scheme = "http"
	}
	if c.Display == "" {
		c.Display = "web"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}
	if c.Port == 0 {
		if c.Scheme == "https" {
			c.Port = 443
		} else {
			c.Port = 80
		}
	}
	if len(c.Url) == 0 {
		return errors.New("no urls defined")
	}
	if c.Scheme == "" {
		c.Scheme = "http"
	}
	for _, u := range c.Url {
		if u.Diff != 0 && u.CompareFile == "" {
			return errors.New("need compare file for diff in web")
		}
		if u.Path == "" {
			u.Path = "/"
		}
	}

	return nil
}
