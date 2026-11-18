package checks

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
)

// heloNameAlphabet is the character set of the random suffix Windows gives new computers (DESKTOP-XXXXXXX).
const heloNameAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// defaultHeloName returns a workstation-style EHLO name that stays the same for a team's check
// across rounds, like a real mail client on one PC. net/smtp would otherwise send "localhost".
func defaultHeloName(teamID uint, checkName string) string {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d/%s", teamID, checkName)
	v := h.Sum64()
	var b strings.Builder
	b.WriteString("DESKTOP-")
	for range 7 {
		b.WriteByte(heloNameAlphabet[v%uint64(len(heloNameAlphabet))])
		v /= uint64(len(heloNameAlphabet))
	}
	return b.String()
}

var (
	mailSubjects = []string{
		"Quick question", "Meeting notes", "Re: schedule for next week", "Lunch on Friday?", "Updated numbers",
		"Follow up", "Re: printer on the 2nd floor", "Status update", "Can you take a look?", "Reminder",
		"Re: shipping delay", "Invoice", "Re: quarterly report", "Out of office tomorrow", "New hire paperwork",
	}
	mailSentences = []string{
		"Just wanted to follow up on what we talked about earlier.",
		"Let me know if you have any questions.",
		"I attached the latest version to the shared drive.",
		"Can we move this to Thursday afternoon?",
		"The numbers look good so far, but I want to double check the totals.",
		"Thanks for getting back to me so quickly.",
		"I will be in the office until about five today.",
		"Please send me the updated list when you get a chance.",
		"The order from the warehouse should arrive by the end of the week.",
		"I talked to the team and everyone is fine with the new plan.",
	}
	mailGreetings = []string{"Hi,", "Hello,", "Hey,", "Good morning,"}
	mailClosings  = []string{"Thanks,", "Best,", "Regards,", "Cheers,"}
)

// composeMessage builds an RFC 5322 message with the headers every mail client writes (Message-ID,
// Date, MIME-Version, From, To, Subject, Content-Type) and a short plain-text body.
func composeMessage(from, to string, now time.Time) string {
	// #nosec G404 -- non-crypto random for email content
	subject := mailSubjects[rand.Intn(len(mailSubjects))]
	var body strings.Builder
	body.WriteString(mailGreetings[rand.Intn(len(mailGreetings))] + "\r\n\r\n") // #nosec G404
	for i, n := 0, 1+rand.Intn(3); i < n; i++ {                                 // #nosec G404
		body.WriteString(mailSentences[rand.Intn(len(mailSentences))] + "\r\n") // #nosec G404
	}
	body.WriteString("\r\n" + mailClosings[rand.Intn(len(mailClosings))] + "\r\n") // #nosec G404
	if name, _, ok := strings.Cut(from, "@"); ok {
		body.WriteString(name + "\r\n")
	}

	domain := "localhost"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	headers := []string{
		"Message-ID: <" + uuid.NewString() + "@" + domain + ">",
		"Date: " + now.Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"From: <" + from + ">",
		"To: <" + to + ">",
		"Subject: " + subject,
		"Content-Type: text/plain; charset=UTF-8; format=flowed",
		"Content-Transfer-Encoding: 7bit",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body.String()
}
