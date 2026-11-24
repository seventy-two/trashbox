// Package compose writes outgoing mail: replies worked out from the message
// being answered, and plain-text messages built as RFC 5322 for sending.
package compose

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/htmltext"
	"github.com/seventy-two/trashbox/engine/internal/mailparse"
)

// Draft is a message being written. Addresses are as typed: "Ann <a@x.com>,
// b@y.com".
type Draft struct {
	From       string   `json:"from"` // the account's address
	To         string   `json:"to"`
	Cc         string   `json:"cc"`
	Bcc        string   `json:"bcc"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	InReplyTo  string   `json:"inReplyTo,omitempty"` // Message-ID without <>
	References []string `json:"references,omitempty"`
}

// Reply drafts an answer to orig from the account me. With all, everyone
// else on the message is copied in.
func Reply(orig *mailparse.Message, me string, all bool) Draft {
	d := Draft{From: me, Subject: prefixed("Re: ", orig.Subject), InReplyTo: orig.MessageID}
	mine := func(a mailparse.Address) bool { return strings.EqualFold(a.Addr, me) }

	var to []mailparse.Address
	switch {
	case len(orig.From) > 0 && mine(orig.From[0]):
		// Answering our own message (from Sent): carry on with its recipients.
		to = orig.To
	case len(orig.ReplyTo) > 0:
		to = orig.ReplyTo
	default:
		to = orig.From
	}
	var cc []mailparse.Address
	if all {
		cc = append(append(cc, orig.To...), orig.Cc...)
	}
	seen := map[string]bool{strings.ToLower(me): true}
	d.To = joinAddrs(dedupe(to, seen, len(orig.From) > 0 && mine(orig.From[0])))
	d.Cc = joinAddrs(dedupe(cc, seen, false))

	refs := append([]string(nil), orig.References...)
	if orig.MessageID != "" {
		refs = append(refs, orig.MessageID)
	}
	if len(refs) > 20 { // the first and the latest are what matter for threading
		refs = append(refs[:1], refs[len(refs)-19:]...)
	}
	d.References = refs
	d.Body = "\n\n" + Quote(orig)
	return d
}

// dedupe drops addresses already in seen (and adds the rest to it). keepMe
// lets our own address through: replying to a message we sent to ourselves.
func dedupe(list []mailparse.Address, seen map[string]bool, keepMe bool) []mailparse.Address {
	var out []mailparse.Address
	for _, a := range list {
		k := strings.ToLower(strings.TrimSpace(a.Addr))
		if k == "" || (seen[k] && !keepMe) {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}

func joinAddrs(list []mailparse.Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a.Name == "" || strings.EqualFold(a.Name, a.Addr) {
			parts = append(parts, a.Addr)
			continue
		}
		name := a.Name
		if strings.ContainsAny(name, `,;<>@"()[]:\`) {
			name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
		}
		parts = append(parts, name+" <"+a.Addr+">")
	}
	return strings.Join(parts, ", ")
}

// prefixed adds "Re: " (or "Fwd: ") unless the subject already starts so.
func prefixed(prefix, subject string) string {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix)) {
		return s
	}
	return prefix + s
}

// Quote is the original message's text under an "On … wrote:" line, each
// line marked with "> ".
func Quote(orig *mailparse.Message) string {
	text := orig.Text
	if strings.TrimSpace(text) == "" {
		text = htmltext.Convert(orig.HTML)
	}
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n ")
	who := "someone"
	if len(orig.From) > 0 {
		who = orig.From[0].Name
		if who == "" {
			who = orig.From[0].Addr
		}
	}
	var b strings.Builder
	if !orig.Date.IsZero() {
		fmt.Fprintf(&b, "On %s, %s wrote:\n", orig.Date.Local().Format("Mon 2 Jan 2006 at 15:04"), who)
	} else {
		fmt.Fprintf(&b, "%s wrote:\n", who)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, ">") {
			b.WriteString(">" + line + "\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

// ParseList reads a typed list of addresses. Commas or semicolons separate
// them; a bare address or "Name <address>" both work.
func ParseList(s string) ([]*mail.Address, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ";", ","))
	if s == "" {
		return nil, nil
	}
	if list, err := mail.ParseAddressList(s); err == nil {
		return list, nil
	}
	// Go through one at a time to name the bad one.
	var out []*mail.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, err := mail.ParseAddress(part)
		if err != nil {
			return nil, fmt.Errorf("%q isn't an email address", part)
		}
		out = append(out, a)
	}
	return out, nil
}

// Outgoing is a message ready to send.
type Outgoing struct {
	From  string
	Rcpts []string // every recipient, Bcc included: the SMTP envelope
	Raw   []byte   // RFC 5322, without a Bcc header
	// BccHeader is "Bcc: …\r\n", for providers that read recipients from
	// the message itself (Graph); empty when there's no Bcc.
	BccHeader string
}

// Build checks a draft and turns it into a message.
func Build(d Draft, now time.Time) (Outgoing, error) {
	if !strings.Contains(d.From, "@") {
		return Outgoing{}, errors.New("no account to send from")
	}
	var lists [3][]*mail.Address
	for i, s := range []string{d.To, d.Cc, d.Bcc} {
		l, err := ParseList(s)
		if err != nil {
			return Outgoing{}, err
		}
		lists[i] = l
	}
	to, cc, bcc := lists[0], lists[1], lists[2]
	if len(to)+len(cc)+len(bcc) == 0 {
		return Outgoing{}, errors.New("who's it to? add an address")
	}
	o := Outgoing{From: d.From}
	for _, l := range lists {
		for _, a := range l {
			o.Rcpts = append(o.Rcpts, a.Address)
		}
	}

	id := make([]byte, 12)
	rand.Read(id)
	host := d.From[strings.LastIndex(d.From, "@")+1:]
	var b strings.Builder
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", (&mail.Address{Address: d.From}).String())
	if len(to) > 0 {
		header("To", addrHeader(to))
	}
	if len(cc) > 0 {
		header("Cc", addrHeader(cc))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", strings.TrimSpace(d.Subject)))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+host+">")
	if d.InReplyTo != "" {
		header("In-Reply-To", "<"+d.InReplyTo+">")
	}
	if len(d.References) > 0 {
		refs := make([]string, len(d.References))
		for i, r := range d.References {
			refs[i] = "<" + r + ">"
		}
		header("References", strings.Join(refs, "\r\n "))
	}
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	body := strings.ReplaceAll(d.Body, "\r\n", "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	qp := quotedprintable.NewWriter(&b)
	qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	qp.Close()
	o.Raw = []byte(b.String())
	if len(bcc) > 0 {
		o.BccHeader = "Bcc: " + addrHeader(bcc) + "\r\n"
	}
	return o, nil
}

func addrHeader(list []*mail.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String() // RFC 2047 encodes names that need it
	}
	return strings.Join(parts, ",\r\n ")
}
