// Package compose writes outgoing mail: replies worked out from the message
// being answered, and plain-text messages built as RFC 5322 for sending.
package compose

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
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
	// Attachments are files on this PC, read when the message is built.
	Attachments []string `json:"attachments,omitempty"`
}

// MaxAttach is the most attached files can add up to: what Gmail takes in
// one message (Outlook.com allows about the same).
const MaxAttach = 25 << 20

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

// File is an attached file.
type File struct {
	Name, Type string
	Data       []byte
}

// Outgoing is a message ready to send.
type Outgoing struct {
	From  string
	Rcpts []string // every recipient, Bcc included: the SMTP envelope
	Raw   []byte   // RFC 5322 with any attachments, without a Bcc header
	// Text is Raw without the attachments, and Files those, for providers
	// that take big files separately (Graph).
	Text  []byte
	Files []File
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

	for _, path := range d.Attachments {
		f, err := readFile(path)
		if err != nil {
			return Outgoing{}, err
		}
		o.Files = append(o.Files, f)
	}
	total := 0
	for _, f := range o.Files {
		total += len(f.Data)
	}
	if total > MaxAttach {
		return Outgoing{}, fmt.Errorf("the attachments come to %.1f MB; the most one message can carry is %d MB",
			float64(total)/(1<<20), MaxAttach>>20)
	}

	id := make([]byte, 12)
	rand.Read(id)
	host := d.From[strings.LastIndex(d.From, "@")+1:]
	var h strings.Builder
	header := func(k, v string) { fmt.Fprintf(&h, "%s: %s\r\n", k, v) }
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
	head := h.String()

	body := strings.ReplaceAll(d.Body, "\r\n", "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	var text bytes.Buffer
	qp := quotedprintable.NewWriter(&text)
	qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	qp.Close()
	const textHead = "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n"
	o.Text = []byte(head + textHead + "\r\n" + text.String())

	if len(o.Files) == 0 {
		o.Raw = o.Text
	} else {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		b.WriteString(head + "Content-Type: multipart/mixed;\r\n boundary=\"" + mw.Boundary() + "\"\r\n\r\n")
		tp, _ := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		tp.Write(text.Bytes())
		for _, f := range o.Files {
			fp, _ := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {mime.FormatMediaType(f.Type, map[string]string{"name": f.Name})},
				"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": f.Name})},
				"Content-Transfer-Encoding": {"base64"},
			})
			writeBase64(fp, f.Data)
		}
		mw.Close()
		o.Raw = b.Bytes()
	}
	if len(bcc) > 0 {
		o.BccHeader = "Bcc: " + addrHeader(bcc) + "\r\n"
	}
	return o, nil
}

// readFile reads an attachment, working out its type from its name (or
// its first bytes).
func readFile(path string) (File, error) {
	st, err := os.Stat(path)
	if err != nil {
		return File{}, fmt.Errorf("can't attach %s: %w", filepath.Base(path), err)
	}
	if st.IsDir() {
		return File{}, fmt.Errorf("%s is a folder: attach the files in it", filepath.Base(path))
	}
	if st.Size() > MaxAttach {
		return File{}, fmt.Errorf("%s is %.1f MB; the most one message can carry is %d MB",
			filepath.Base(path), float64(st.Size())/(1<<20), MaxAttach>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("can't attach %s: %w", filepath.Base(path), err)
	}
	name := filepath.Base(path)
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mt // no charset: the bytes go as they are
	}
	return File{Name: name, Type: ct, Data: data}, nil
}

// writeBase64 writes data base64-encoded in 76-character lines.
func writeBase64(w io.Writer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		io.WriteString(w, enc[:76]+"\r\n")
		enc = enc[76:]
	}
	io.WriteString(w, enc+"\r\n")
}

func addrHeader(list []*mail.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String() // RFC 2047 encodes names that need it
	}
	return strings.Join(parts, ",\r\n ")
}
