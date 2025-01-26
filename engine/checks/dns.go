package checks

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type Dns struct {
	Service
	Record []DnsRecord
}

type DnsRecord struct {
	Kind   string
	Domain string
	Answer []string
}

func (c Dns) Run(teamID uint, teamIdentifier string, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Pick a record
		record := c.Record[rand.Intn(len(c.Record))]
		fqdn := dns.Fqdn(strings.ReplaceAll(dns.Fqdn(record.Domain), "_", teamIdentifier))

		// Setup for dns query
		var msg dns.Msg

		// switch of kind of record (A, MX, etc)
		// TODO: add more values
		switch record.Kind {
		case "A":
			msg.SetQuestion(fqdn, dns.TypeA)
		case "MX":
			msg.SetQuestion(fqdn, dns.TypeMX)
		}

		// Make it obey timeout via deadline
		// deadctx, cancel := context.WithTimeout(context.TODO(), time.Duration(2)*time.Second)
		// defer cancel()

		// Send the query
		client := dns.Client{Timeout: time.Duration(c.Timeout-1) * time.Second, DialTimeout: time.Duration(c.Timeout-1) * time.Second}
		// _, _ = dns.ExchangeContext(deadctx, &msg, fmt.Sprintf("%s:%d", c.Target, c.Port)) // double tap for propagation
		in, rtt, err := client.Exchange(&msg, fmt.Sprintf("%s:%d", c.Target, c.Port))
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				in, rtt, err = client.Exchange(&msg, fmt.Sprintf("%s:%d", c.Target, c.Port))
				if err != nil {
					checkResult.Error = "error sending query"
					checkResult.Debug = "record " + record.Domain + ":" + fmt.Sprint(record.Answer) + fmt.Sprintf("(took %s)", rtt) + ": " + err.Error()
					response <- checkResult
					return
				}
			} else {
				checkResult.Error = "error sending query"
				checkResult.Debug = "record " + record.Domain + ":" + fmt.Sprint(record.Answer) + fmt.Sprintf("(took %s)", rtt) + ": " + err.Error()
				response <- checkResult
				return
			}
		}

		// Check if we got any records
		if len(in.Answer) < 1 {
			checkResult.Error = "no records received"
			checkResult.Debug = "record " + record.Domain + "-> " + fmt.Sprint(record.Answer)
			response <- checkResult
			return
		}

		// Loop through results and check for correct match
		for _, answer := range in.Answer {
			// Check if an answer is an A record and it matches the expected IP
			for _, expectedAnswer := range record.Answer {
				expectedAnswer = strings.ReplaceAll(expectedAnswer, "_", teamIdentifier)
				if a, ok := answer.(*dns.A); ok && (a.A).String() == expectedAnswer {
					checkResult.Status = true
					checkResult.Debug = fmt.Sprintf("record %s returned %s. acceptable answers were: %v", record.Domain, expectedAnswer, record.Answer)
					response <- checkResult
					return
				}
			}
		}

		// If we reach here no records matched expected IP and check fails
		checkResult.Error = "incorrect answer(s) received from DNS"
		checkResult.Debug = "record " + record.Domain + "-> acceptable answers were: " + fmt.Sprint(record.Answer) + ", received " + fmt.Sprint(in.Answer)
		response <- checkResult
	}

	c.Service.Run(teamID, teamIdentifier, resultsChan, definition)
}

func (c *Dns) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Dns"
	}
	if err := c.Service.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
		return err
	}
	if c.Port == 0 {
		c.Port = 53
	}
	if len(c.Record) < 1 {
		return errors.New("dns check " + c.Name + " has no records")
	}
	if c.Display == "" {
		c.Display = "dns"
	}
	if c.Name == "" {
		c.Name = box + "-" + c.Display
	}

	return nil
}
