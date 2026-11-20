package checks

import (
	"encoding/binary"
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

func (c Dns) Run(teamID uint, teamIdentifier string, roundID uint, resultsChan chan Result) {
	definition := func(teamID uint, teamIdentifier string, checkResult Result, response chan Result) {
		// Pick a record
		record := c.Record[rand.Intn(len(c.Record))] // #nosec G404 -- non-crypto selection of DNS record to test
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
		in, rtt, err := exchangeLikeStub(&client, &msg, fmt.Sprintf("%s:%d", c.Target, c.Port))
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				in, rtt, err = exchangeLikeStub(&client, &msg, fmt.Sprintf("%s:%d", c.Target, c.Port))
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
			// Check if an answer matches an expected value for its record type
			for _, expectedAnswer := range record.Answer {
				expectedAnswer = strings.ReplaceAll(expectedAnswer, "_", teamIdentifier)
				if dnsAnswerMatches(answer, expectedAnswer) {
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

	c.Service.Run(teamID, teamIdentifier, roundID, resultsChan, definition)
}

// dnsAnswerMatches compares an A answer's address, or an MX answer's mail
// exchanger (case-insensitively, as a fully qualified name), to expected.
func dnsAnswerMatches(answer dns.RR, expected string) bool {
	switch rr := answer.(type) {
	case *dns.A:
		return rr.A.String() == expected
	case *dns.MX:
		return strings.EqualFold(rr.Mx, dns.Fqdn(expected))
	}
	return false
}

// dnsDefaultTimeout is miekg/dns's read and write timeout for a Client without a Timeout.
const dnsDefaultTimeout = 2 * time.Second

// exchangeLikeStub sends msg the way the glibc stub resolver (getaddrinfo) resolves a host name:
// an A question is followed straight away by an AAAA question for the same name on the same
// socket. Only the reply to msg is returned: packets with another ID (the AAAA reply) or for
// another question type that arrive before it are skipped. Other query types go out alone.
func exchangeLikeStub(client *dns.Client, msg *dns.Msg, addr string) (*dns.Msg, time.Duration, error) {
	if len(msg.Question) != 1 || msg.Question[0].Qtype != dns.TypeA {
		return client.Exchange(msg, addr)
	}
	co, err := client.Dial(addr)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = co.Close() }()

	timeout := client.Timeout
	if timeout == 0 {
		timeout = dnsDefaultTimeout
	}
	start := time.Now()
	if err := co.SetDeadline(start.Add(timeout)); err != nil {
		return nil, 0, err
	}
	if err := co.WriteMsg(msg); err != nil {
		return nil, 0, err
	}
	var aaaa dns.Msg
	aaaa.SetQuestion(msg.Question[0].Name, dns.TypeAAAA)
	for aaaa.Id == msg.Id {
		aaaa.Id = dns.Id()
	}
	if err := co.WriteMsg(&aaaa); err != nil {
		return nil, 0, err
	}
	p := make([]byte, dns.MinMsgSize)
	for {
		n, err := co.Read(p)
		if err != nil {
			return nil, time.Since(start), err
		}
		if n < 2 || binary.BigEndian.Uint16(p) != msg.Id {
			continue
		}
		// A reply with msg's ID that does not parse fails the query, as it does in Exchange.
		in := new(dns.Msg)
		if err := in.Unpack(p[:n]); err != nil {
			return nil, time.Since(start), err
		}
		// Exchange accepts any question section; only a reply for another question type is skipped.
		if len(in.Question) > 0 && in.Question[0].Qtype != dns.TypeA {
			continue
		}
		return in, time.Since(start), nil
	}
}

func (c *Dns) Verify(box string, ip string, points int, timeout int, slapenalty int, slathreshold int) error {
	if c.ServiceType == "" {
		c.ServiceType = "Dns"
	}
	if err := c.Configure(ip, points, timeout, slapenalty, slathreshold); err != nil {
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
	for _, record := range c.Record {
		if record.Kind != "A" && record.Kind != "MX" {
			return fmt.Errorf("dns check %s: unsupported record kind %q (supported: A, MX)", c.Name, record.Kind)
		}
	}

	return nil
}
