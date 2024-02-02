package checks

import (
	"context"
	"fmt"
	"math/rand"
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

func (c Dns) Run(teamID uint, boxIp string, res chan Result, service Service) {
	// Pick a record
	record := c.Record[rand.Intn(len(c.Record))]
	fqdn := dns.Fqdn(record.Domain)
	answerList := fmt.Sprint(record.Answer)

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
	deadctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(GlobalTimeout))
	defer cancel()

	// Send the query
	in, err := dns.ExchangeContext(deadctx, &msg, fmt.Sprintf("%s:%d", boxIp, c.Port))
	if err != nil {
		res <- Result{
			Error: "error sending query",
			Debug: "record " + record.Domain + ":" + answerList + ": " + err.Error(),
		}
		return
	}

	// Check if we got any records
	if len(in.Answer) < 1 {
		res <- Result{
			Error: "no records received",
			Debug: "record " + record.Domain + "-> " + answerList,
		}
		return
	}

	// Loop through results and check for correct match
	for _, answer := range in.Answer {
		// Check if an answer is an A record and it matches the expected IP
		for _, expectedAnswer := range record.Answer {
			if a, ok := answer.(*dns.A); ok && (a.A).String() == expectedAnswer {
				res <- Result{
					Status: true,
					Error:  "record " + record.Domain + " returned " + expectedAnswer,
					Debug:  "acceptable answers were: " + answerList,
				}
				return
			}
		}
	}

	// If we reach here no records matched expected IP and check fails
	res <- Result{
		Error: "incorrect answer(s) received from DNS",
		Debug: "acceptable answers were: " + answerList + "," + " received " + fmt.Sprint(in.Answer),
	}
}
