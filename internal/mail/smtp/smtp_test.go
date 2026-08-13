// SPDX-License-Identifier: GPL-3.0-or-later

// The SMTP adapter's own tests. Until these existed it had none at all, which
// is worth saying plainly: it is the adapter production runs, and the only
// thing exercising it was an integration test one package up that was really
// testing the server.
//
// A stub speaks the protocol over a real socket rather than a mock standing in
// for net/smtp. The adapter's job is almost entirely conversation — EHLO, MAIL,
// RCPT, DATA, the dot that ends the body — and a mock of the client asserts
// that we call the functions we already know we call. Mailpit still runs in the
// integration suite for the part this cannot prove: that a real mail server
// accepts what we compose.
package smtp_test

import (
	"bufio"
	"context"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/alrayyes/form-handler/internal/mail/smtp"
)

// refuseAt names the step a stub turns the message away at. Empty accepts the
// whole conversation.
type refuseAt string

const (
	acceptEverything refuseAt = ""
	refuseRecipient  refuseAt = "rcpt"
)

// stubSMTP speaks enough SMTP to finish a send, and hands back the address to
// point a Sender at. Deliberately does not advertise STARTTLS or AUTH: the
// adapter asks for both and has to cope with a server offering neither, which
// is exactly the local mail bridge it was written for.
func stubSMTP(t *testing.T, refuse refuseAt) (addr string, delivered *received) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	got := &received{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // the listener closed; the test is over
			}
			go converse(conn, refuse, got)
		}
	}()

	return ln.Addr().String(), got
}

// received is what the stub was told, so a test can assert on the message that
// actually crossed the socket rather than on the struct that produced it.
type received struct {
	mu   sync.Mutex
	body strings.Builder
	from string
	to   string
}

func (r *received) Body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func (r *received) Envelope() (from, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.from, r.to
}

func converse(conn net.Conn, refuse refuseAt, got *received) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	say("220 stub ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, rest, _ := strings.Cut(line, " ")

		switch strings.ToUpper(verb) {
		// A single-line 250 is a valid EHLO answer and advertises nothing,
		// which is the point: no STARTTLS, no AUTH.
		case "EHLO", "HELO":
			say("250 stub")
		case "MAIL":
			got.mu.Lock()
			got.from = rest
			got.mu.Unlock()
			say("250 OK")
		case "RCPT":
			if refuse == refuseRecipient {
				say("550 no mailbox by that name")
				continue
			}
			got.mu.Lock()
			got.to = rest
			got.mu.Unlock()
			say("250 OK")
		case "DATA":
			say("354 go ahead")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(l, "\r\n") == "." {
					break
				}
				got.mu.Lock()
				got.body.WriteString(l)
				got.mu.Unlock()
			}
			say("250 OK")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("500 unrecognised")
		}
	}
}

func sender(addr string) smtp.Sender {
	return smtp.Sender{
		Addr:    addr,
		From:    "site@example.com",
		To:      "info@example.com",
		Timeout: 5 * time.Second,
	}
}

// The contract every Mailer keeps, run against this one. A new adapter is one
// call to this away from being held to the same standard.
func TestTheSenderKeepsTheMailerContract(t *testing.T) {
	mailertest.Contract(t, mailertest.Subject{
		Provider: "smtp",
		Working: func(t *testing.T) contact.Mailer {
			addr, _ := stubSMTP(t, acceptEverything)
			return sender(addr)
		},
		Failing: func(t *testing.T) contact.Mailer {
			addr, _ := stubSMTP(t, refuseRecipient)
			return sender(addr)
		},
	})
}

// The shared contract can only ask whether *something* is under the
// DeliveryError, because it has no idea what this adapter talks to. Here we do:
// net/smtp reports a refusal as a *textproto.Error carrying the server's own
// code, and that has to survive the wrapping. An adapter rebuilding it as
// errors.New(err.Error()) would satisfy the contract and still leave a caller
// with a sentence to parse instead of a 550.
func TestARefusalKeepsTheServersOwnReplyReachable(t *testing.T) {
	addr, _ := stubSMTP(t, refuseRecipient)

	err := sender(addr).Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	var pe *textproto.Error
	require.ErrorAs(t, err, &pe, "the mail server's own reply was flattened into a string")
	assert.Equal(t, 550, pe.Code)
}

// A mail server that is not there is the ordinary production failure — the
// bridge restarted, the container is not up yet — and it has to arrive as the
// same shape of error as a refusal does.
func TestAnUnreachableServerIsReportedAsDial(t *testing.T) {
	err := sender("127.0.0.1:1").Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	var de *contact.DeliveryError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "dial", de.Op)
}

// The visitor's address is where a reply goes, never who the mail claims to be
// from. Sending as them fails SPF for their domain and files the whole thing as
// spam. Same rule the Mailgun adapter follows.
func TestTheVisitorIsTheReplyToNotTheSender(t *testing.T) {
	addr, delivered := stubSMTP(t, acceptEverything)

	require.NoError(t, sender(addr).Send(context.Background(), contact.Message{
		Name: "Ada Lovelace", Email: "ada@example.com", Subject: "Contact form", Body: "hello there",
	}))

	assert.Contains(t, delivered.Body(), "Reply-To: ada@example.com")
	assert.Contains(t, delivered.Body(), "From: site@example.com")
}

// A newline in a header is how injection works, and the subject is built from
// whatever the visitor typed their name as.
//
// The words survive — the subject reads "Contact formBcc: everyone@example.com"
// and looks odd to whoever opens it. That is fine and is not what this guards.
// What must not survive is the line break, because that is the character that
// turns the rest of the string into a header of its own.
func TestAHeaderCannotBeSmuggledIntoTheSubject(t *testing.T) {
	addr, delivered := stubSMTP(t, acceptEverything)

	require.NoError(t, sender(addr).Send(context.Background(), contact.Message{
		Name:    "Ada",
		Email:   "ada@example.com",
		Subject: "Contact form\r\nBcc: everyone@example.com",
		Body:    "hello there",
	}))

	assert.NotContains(t, delivered.Body(), "\r\nBcc:", "the subject started a header of its own")
}

// The envelope is the addresses the mail server routes on, and SPF is checked
// against the envelope sender. Putting the visitor there would fail SPF for
// their domain and file the message as spam, so it carries ours in both
// directions and the visitor appears only in Reply-To.
func TestTheEnvelopeCarriesOurAddressesNotTheVisitors(t *testing.T) {
	addr, delivered := stubSMTP(t, acceptEverything)

	require.NoError(t, sender(addr).Send(context.Background(), contact.Message{
		Name: "Ada Lovelace", Email: "ada@example.com", Subject: "Contact form", Body: "hello there",
	}))

	from, to := delivered.Envelope()
	assert.Contains(t, from, "site@example.com")
	assert.Contains(t, to, "info@example.com")
	assert.NotContains(t, from, "ada@example.com", "the visitor was made the envelope sender")
}

// SMTP ends a message with a lone dot on its own line. A body containing one
// would otherwise cut the mail short and leave the rest to be read as commands.
func TestALoneDotInTheBodyDoesNotEndTheMessage(t *testing.T) {
	addr, delivered := stubSMTP(t, acceptEverything)

	require.NoError(t, sender(addr).Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s",
		Body: "first paragraph\n.\nsecond paragraph",
	}))

	assert.Contains(t, delivered.Body(), "second paragraph")
}
