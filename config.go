package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"sugmaase/checks"

	"golang.org/x/exp/slices"

	"github.com/BurntSushi/toml"
)

var (
	configErrors    = []string{}
	supportedEvents = []string{"rvb", "koth"} // golang doesn't have constant arrays :/
)

type Config struct {
	// General engine settings
	Event         string
	EventType     string
	DBConnectURL  string
	Timezone      string
	JWTPrivateKey string
	JWTPublicKey  string
	// Optional settings
	EasyPCR     bool
	Verbose     bool
	Port        int
	Https       bool
	Cert        string `toml:"cert,omitempty" json:"cert,omitempty"`
	Key         string `toml:"key,omitempty" json:"key,omitempty"`
	StartPaused bool
	// Restrict information
	DisableInfoPage      bool
	DisableHeadToHead    bool
	DisableExternalPorts bool

	// Round settings
	Delay  int
	Jitter int

	// Defaults for checks
	Timeout       int
	SlaThreshold  int
	ServicePoints int
	SlaPoints     int

	Admin []Admin
	Box   []Box
}

// mostly used for Form and NOT FOR DATABASE... maybe change later
type Admin struct {
	ID   uint
	Name string
	Pw   string
}

type Box struct {
	Name string
	IP   string

	// Only used for delayed check
	Time time.Time `gorm:"-" toml:"-"`

	CheckList []checks.ServiceHandler `toml:"check,omitempty" json:"-"`
	Custom    []checks.Custom         `toml:"custom,omitempty" json:"custom,omitempty"`
	Dns       []checks.Dns            `toml:"dns,omitempty" json:"dns,omitempty"`
	Ftp       []checks.Ftp            `toml:"ftp,omitempty" json:"ftp,omitempty"`
	Imap      []checks.Imap           `toml:"imap,omitempty" json:"imap,omitempty"`
	Ldap      []checks.Ldap           `toml:"ldap,omitempty" json:"ldap,omitempty"`
	Ping      []checks.Ping           `toml:"ping,omitempty" json:"ping,omitempty"`
	Rdp       []checks.Rdp            `toml:"rdp,omitempty" json:"rdp,omitempty"`
	Smb       []checks.Smb            `toml:"smb,omitempty" json:"smb,omitempty"`
	Smtp      []checks.Smtp           `toml:"smtp,omitempty" json:"smtp,omitempty"`
	Sql       []checks.Sql            `toml:"sql,omitempty" json:"sql,omitempty"`
	Ssh       []checks.Ssh            `toml:"ssh,omitempty" json:"ssh,omitempty"`
	Tcp       []checks.Tcp            `toml:"tcp,omitempty" json:"tcp,omitempty"`
	Vnc       []checks.Vnc            `toml:"vnc,omitempty" json:"vnc,omitempty"`
	Web       []checks.Web            `toml:"web,omitempty" json:"web,omitempty"`
	WinRM     []checks.WinRM          `toml:"winrm,omitempty" json:"winrm,omitempty"`
}

func readConfig(path string) Config {
	conf := Config{}
	fileContent, err := os.ReadFile(path)
	if err != nil {
		log.Fatalln("Configuration file ("+path+") not found:", err)
	}
	if md, err := toml.Decode(string(fileContent), &conf); err != nil {
		log.Fatalln(err)
	} else {
		for _, undecoded := range md.Undecoded() {
			errMsg := "[WARN] Undecoded configuration key \"" + undecoded.String() + "\" will not be used."
			configErrors = append(configErrors, errMsg)
			log.Println(errMsg)
		}
	}
	return conf
}

// general error checking
func checkConfig(conf *Config) error {
	// check top level configs
	// required settings
	var errResult error
	if conf.Event == "" {
		errResult = errors.Join(errResult, errors.New("event title blank or not specified"))
	}

	if !slices.Contains(supportedEvents, conf.EventType) {
		errResult = errors.Join(errResult, errors.New("not a valid event type"))
	}

	if conf.DBConnectURL == "" {
		errResult = errors.Join(errResult, errors.New("no db connect url specified"))
	}

	if conf.JWTPrivateKey == "" || conf.JWTPublicKey == "" {
		errResult = errors.Join(errResult, errors.New("missing JWT private/public key pair"))
	}

	if len(conf.Admin) == 0 {
		errResult = errors.Join(errResult, errors.New("missing at least 1 defined admin user"))
	} else {
		for _, admin := range conf.Admin {
			if admin.Name == "" || admin.Pw == "" {
				errResult = errors.Join(errResult, errors.New("admin "+admin.Name+" missing required property"))
			}
		}
	}

	if conf.Timezone == "" {
		errResult = errors.Join(errResult, errors.New("no timezone specified"))
	}

	// optional settings
	if conf.Delay == 0 {
		conf.Delay = 60
	}

	if conf.Jitter == 0 {
		conf.Jitter = 5
	}

	if conf.Https == true {
		if conf.Cert == "" || conf.Key == "" {
			errResult = errors.Join(errResult, errors.New("https requires a cert and key pair"))
		}
	}

	if conf.Port == 0 {
		if conf.Https {
			conf.Port = 443
		} else {
			conf.Port = 80
		}
	}

	if conf.Jitter >= conf.Delay {
		errResult = errors.Join(errResult, errors.New("jitter must be smaller than delay"))
	}

	if conf.Timeout != 0 {
		dur := time.Duration(conf.Timeout) * time.Second
		checks.GlobalTimeout = dur
	} else {
		checks.GlobalTimeout = time.Second * 30
	}
	if conf.Timeout >= conf.Delay-conf.Jitter {
		errResult = errors.Join(errResult, errors.New("timeout must be smaller than delay minus jitter"))
	}

	if conf.ServicePoints == 0 {
		conf.ServicePoints = 1
	}

	if conf.SlaThreshold == 0 {
		conf.SlaThreshold = 5
	}

	if conf.SlaPoints == 0 {
		conf.SlaPoints = conf.SlaThreshold * conf.ServicePoints
	}

	if conf.StartPaused {
		enginePauseWg.Add(1)
		enginePause = true
	}

	// =======================================
	// prepare for box config checking
	// sort boxes
	sort.SliceStable(conf.Box, func(i, j int) bool {
		return conf.Box[i].IP < conf.Box[j].IP
	})

	// check for duplicate box names
	dupeBoxMap := make(map[string]bool)
	for _, b := range conf.Box {
		if b.Name == "" {
			return errors.New("a box is missing a name")
		}
		if _, ok := dupeBoxMap[b.Name]; ok {
			return errors.New("duplicate box name found: " + b.Name)
		}
	}

	// look for duplicate checks
	for _, b := range conf.Box {
		for j := 0; j < len(b.CheckList)-1; j++ {
			if b.CheckList[j].Name == b.CheckList[j+1].Name {
				return errors.New("duplicate check name '" + b.CheckList[j].Name + "' and '" + b.CheckList[j+1].Name + "' for box " + b.Name)
			}
		}
	}

	// ACTUALLY DO CHECKS FOR BOXES AND SERVICES
	err := validateChecks(conf.Box)
	if err != nil {
		return err
	}

	// errResult is nil by default if no errors occured
	return errResult
}

func validateChecks(boxes []Box) error {
	// check validators
	// please overlook this transgression
	for i, box := range boxes {
		if box.Name == "" {
			return errors.New(fmt.Sprintf("no name found for box %d", i))
		}

		if box.IP == "" {
			return errors.New("no ip found for box " + box.Name)
		}
		// Ensure IP replacement chars are lowercase
		box.IP = strings.ToLower(box.IP)
		boxes[i].IP = box.IP

		boxes[i].CheckList = getBoxChecks(box)
		if len(boxes[i].CheckList) == 0 {
			continue
		}
		earliestCheck := boxes[i].CheckList[0].StopTime
		for j, check := range boxes[i].CheckList {
			if check.Points == 0 {
				check.Points = eventConf.ServicePoints
			}
			if check.SlaThreshold == 0 {
				check.SlaThreshold = eventConf.SlaThreshold
			}
			if check.SlaPenalty == 0 {
				check.SlaPenalty = eventConf.SlaThreshold
			}
			if check.LaunchTime.Before(earliestCheck) {
				earliestCheck = check.LaunchTime
			}
			if check.StopTime.IsZero() {
				check.StopTime = time.Now().AddDate(1, 0, 0) // one year ahead should be far enough
			}
			check.Type = reflect.TypeOf(check.Runner).String()
			switch check.Runner.(type) {
			case checks.Custom:
				ck := check.Runner.(checks.Custom)
				if check.Display == "" {
					check.Display = "custom"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if len(check.CredLists) < 1 && !strings.Contains(ck.Command, "USERNAME") && !strings.Contains(ck.Command, "PASSWORD") {
					check.Anonymous = true
				}
				check.Runner = ck
			case checks.Dns:
				ck := check.Runner.(checks.Dns)
				check.IP = box.IP
				check.Anonymous = true // call me when you need authed DNS
				if check.Display == "" {
					check.Display = "dns"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if len(ck.Record) < 1 {
					return errors.New("dns check " + check.Name + " has no records")
				}
				if check.Port == 0 {
					check.Port = 53
				}
				check.Runner = ck
			case checks.Ftp:
				ck := check.Runner.(checks.Ftp)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "ftp"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 21
				}
				for _, f := range ck.File {
					if f.Regex != "" && f.Hash != "" {
						return errors.New("can't have both regex and hash for ftp file check")
					}
				}
				check.Runner = ck
			case checks.Imap:
				ck := check.Runner.(checks.Imap)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "imap"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 143
				}
				check.Runner = ck
			case checks.Ldap:
				ck := check.Runner.(checks.Ldap)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "ldap"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 636
				}
				if check.Anonymous {
					return errors.New("anonymous ldap not supported")
				}
				check.Runner = ck
			case checks.Ping:
				ck := check.Runner.(checks.Ping)
				check.IP = box.IP
				check.Anonymous = true
				if ck.Count == 0 {
					ck.Count = 1
				}
				if check.Display == "" {
					check.Display = "ping"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				check.Runner = ck
			case checks.Rdp:
				ck := check.Runner.(checks.Rdp)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "rdp"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 3389
				}
				check.Runner = ck
			case checks.Smb:
				ck := check.Runner.(checks.Smb)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "smb"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 445
				}
				check.Runner = ck
			case checks.Smtp:
				ck := check.Runner.(checks.Smtp)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "smtp"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 25
				}
				check.Runner = ck
			case checks.Sql:
				ck := check.Runner.(checks.Sql)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "sql"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if ck.Kind == "" {
					ck.Kind = "mysql"
				}
				if check.Port == 0 {
					check.Port = 3306
				}
				for _, q := range ck.Query {
					if q.DatabaseExists && (q.Column != "" || q.Table != "" || q.Output != "") {
						return errors.New("cannot use both database exists check and row check")
					}
					if q.DatabaseExists && q.Database == "" {
						return errors.New("must specify database for database exists check")
					}
					if q.UseRegex {
						regexp.MustCompile(q.Output)
					}
					if q.UseRegex && q.Contains {
						return errors.New("cannot use both regex and contains")
					}
				}
				check.Runner = ck
			case checks.Ssh:
				ck := check.Runner.(checks.Ssh)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "ssh"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 22
				}
				if ck.PrivKey != "" && ck.BadAttempts != 0 {
					return errors.New("can not have bad attempts with pubkey for ssh")
				}
				for _, r := range ck.Command {
					if r.UseRegex {
						regexp.MustCompile(r.Output)
					}
					if r.UseRegex && r.Contains {
						return errors.New("cannot use both regex and contains")
					}
				}
				if check.Anonymous {
					return errors.New("anonymous ssh not supported")
				}
				check.Runner = ck
			case checks.Tcp:
				ck := check.Runner.(checks.Tcp)
				check.IP = box.IP
				check.Anonymous = true
				if check.Display == "" {
					check.Display = "tcp"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					return errors.New("tcp port required")
				}
				check.Runner = ck
			case checks.Vnc:
				ck := check.Runner.(checks.Vnc)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "vnc"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 5900
				}
				check.Runner = ck
			case checks.Web:
				ck := check.Runner.(checks.Web)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "web"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					check.Port = 80
				}
				if len(ck.Url) == 0 {
					return errors.New("no urls specified for web check " + check.Name)
				}
				if len(check.CredLists) < 1 {
					check.Anonymous = true
				}
				if ck.Scheme == "" {
					ck.Scheme = "http"
				}
				for _, u := range ck.Url {
					if u.Diff != 0 && u.CompareFile == "" {
						return errors.New("need compare file for diff in web")
					}
				}
				check.Runner = ck
			case checks.WinRM:
				ck := check.Runner.(checks.WinRM)
				check.IP = box.IP
				if check.Display == "" {
					check.Display = "winrm"
				}
				if check.Name == "" {
					check.Name = box.Name + "-" + check.Display
				}
				if check.Port == 0 {
					if ck.Encrypted {
						check.Port = 443
					} else {
						check.Port = 80
					}
				}
				if check.Anonymous {
					return errors.New("anonymous winrm not supported")
				}
				for _, r := range ck.Command {
					if r.UseRegex {
						regexp.MustCompile(r.Output)
					}
					if r.UseRegex && r.Contains {
						return errors.New("cannot use both regex and contains")
					}
				}
				check.Runner = ck
			}
			boxes[i].CheckList[j] = check
		}
	}
	return nil

}

func getBoxChecks(b Box) []checks.ServiceHandler {
	// Please forgive me
	checkList := []checks.ServiceHandler{}
	for _, c := range b.Custom {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Dns {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Ftp {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Imap {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Ping {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Ldap {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Rdp {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Smb {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Smtp {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Sql {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Ssh {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Tcp {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Vnc {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.Web {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	for _, c := range b.WinRM {
		checkList = append(checkList, checks.ServiceHandler{Service: c.Service, Runner: c})
	}
	return checkList
}
