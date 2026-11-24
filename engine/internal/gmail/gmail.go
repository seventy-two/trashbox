// Package gmail talks to Gmail over IMAP (reading) and SMTP (sending), signed
// in with an app password.
package gmail

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Server addresses. Tests point these at local servers.
type Servers struct {
	IMAP     string // host:port, implicit TLS unless Insecure
	SMTP     string // host:port, implicit TLS unless Insecure
	Insecure bool   // plain TCP, for tests only
}

// Default is Gmail.
var Default = Servers{IMAP: "imap.gmail.com:993", SMTP: "smtp.gmail.com:465"}

// NormalisePassword strips the spaces Google shows app passwords with.
func NormalisePassword(p string) string {
	return strings.ReplaceAll(strings.TrimSpace(p), " ", "")
}

// Summary is the inbox at a glance.
type Summary struct {
	Messages uint32
	Unseen   uint32
	Recent   []Message // newest first
}

// Message is one message in a list.
type Message struct {
	Subject string
	From    string
	Date    time.Time
	Seen    bool
}

func (s Servers) dial() (*imapclient.Client, error) {
	if s.Insecure {
		return imapclient.DialInsecure(s.IMAP, nil)
	}
	return imapclient.DialTLS(s.IMAP, nil)
}

// Check logs in, reads the inbox counts and the newest n envelopes.
func (s Servers) Check(user, pass string, n int) (Summary, error) {
	c, err := s.dial()
	if err != nil {
		return Summary{}, fmt.Errorf("imap connect: %w", err)
	}
	defer c.Close()
	if err := c.Login(user, pass).Wait(); err != nil {
		return Summary{}, fmt.Errorf("imap login: %w", err)
	}
	defer func() { c.Logout().Wait() }()

	st, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true, NumUnseen: true}).Wait()
	if err != nil {
		return Summary{}, fmt.Errorf("imap status: %w", err)
	}
	var sum Summary
	if st.NumMessages != nil {
		sum.Messages = *st.NumMessages
	}
	if st.NumUnseen != nil {
		sum.Unseen = *st.NumUnseen
	}
	if sum.Messages == 0 || n <= 0 {
		return sum, nil
	}

	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return sum, fmt.Errorf("imap select: %w", err)
	}
	first := uint32(1)
	if sum.Messages > uint32(n) {
		first = sum.Messages - uint32(n) + 1
	}
	var set imap.SeqSet
	set.AddRange(first, sum.Messages)
	msgs, err := c.Fetch(set, &imap.FetchOptions{Envelope: true, Flags: true}).Collect()
	if err != nil {
		return sum, fmt.Errorf("imap fetch: %w", err)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].SeqNum > msgs[j].SeqNum })
	for _, m := range msgs {
		if m.Envelope == nil {
			continue
		}
		sum.Recent = append(sum.Recent, Message{
			Subject: m.Envelope.Subject,
			From:    fromName(m.Envelope.From),
			Date:    m.Envelope.Date,
			Seen:    hasFlag(m.Flags, imap.FlagSeen),
		})
	}
	return sum, nil
}

func fromName(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	if addrs[0].Name != "" {
		return addrs[0].Name
	}
	return addrs[0].Addr()
}

func hasFlag(flags []imap.Flag, f imap.Flag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// BuildText builds a plain-text RFC 5322 message.
func BuildText(from string, to []string, subject, body string, now time.Time) []byte {
	id := make([]byte, 12)
	rand.Read(id)
	host := "trashbox.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		host = from[i+1:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), host)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	body = strings.ReplaceAll(body, "\r\n", "\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// SendText sends a plain-text message. Gmail files it in Sent by itself.
func (s Servers) SendText(user, pass string, to []string, subject, body string) error {
	host, _, err := net.SplitHostPort(s.SMTP)
	if err != nil {
		return err
	}
	var conn net.Conn
	if s.Insecure {
		conn, err = net.DialTimeout("tcp", s.SMTP, 30*time.Second)
	} else {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 30 * time.Second}, "tcp", s.SMTP, &tls.Config{ServerName: host})
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer c.Close()
	if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
		return fmt.Errorf("smtp login: %w", err)
	}
	if err := c.Mail(user); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("smtp to %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(BuildText(user, to, subject, body, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return c.Quit()
}
