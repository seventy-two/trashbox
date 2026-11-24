// Package mailparse turns a raw RFC 5322 message into the parts trashbox
// shows: the HTML and plain-text bodies, inline images and attachments.
package mailparse

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 charsets
	"github.com/emersion/go-message/mail"
)

// Address is a name and an email address.
type Address struct {
	Name string `json:"name,omitempty"`
	Addr string `json:"addr"`
}

// Resource is an inline part referenced by Content-ID, usually an image.
type Resource struct {
	Type string
	Data []byte
}

// Attachment is a part the user can save.
type Attachment struct {
	Index int    `json:"index"` // position among attachments, for fetching it later
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int    `json:"size"`
}

// Message is a parsed email.
type Message struct {
	From        []Address
	To          []Address
	Cc          []Address
	ReplyTo     []Address
	Subject     string
	Date        time.Time
	MessageID   string
	References  []string
	HTML        string              // UTF-8
	Text        string              // UTF-8
	Inline      map[string]Resource // by Content-ID without <>
	Attachments []Attachment
	attachData  [][]byte
}

// AttachmentData returns attachment i's bytes.
func (m *Message) AttachmentData(i int) ([]byte, bool) {
	if i < 0 || i >= len(m.attachData) {
		return nil, false
	}
	return m.attachData[i], true
}

// maxPart caps one part's size, so a huge attachment can't eat the memory.
const maxPart = 64 << 20

// Parse reads a raw message. Bad parts are skipped rather than failing the
// whole message: a mail client must show what it can.
func Parse(raw []byte) (*Message, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && r == nil {
		return nil, err
	}
	defer r.Close()

	m := &Message{Inline: map[string]Resource{}}
	h := r.Header
	m.From = addrs(h, "From")
	m.To = addrs(h, "To")
	m.Cc = addrs(h, "Cc")
	m.ReplyTo = addrs(h, "Reply-To")
	m.Subject, _ = h.Subject()
	m.Date, _ = h.Date()
	m.MessageID, _ = h.MessageID()
	m.References, _ = h.MsgIDList("References")

	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if p == nil {
				break // the structure is broken past this point
			}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(p.Body, maxPart))
		if err != nil {
			continue
		}
		switch ph := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := ph.ContentType()
			cid := strings.Trim(ph.Get("Content-Id"), "<> ")
			switch {
			case ct == "text/html" && m.HTML == "":
				m.HTML = string(data)
			case ct == "text/plain" && m.Text == "":
				m.Text = string(data)
			case cid != "":
				m.Inline[cid] = Resource{Type: ct, Data: data}
			case strings.HasPrefix(ct, "text/"):
				// a second body part, e.g. a signature block: keep it with the text
				if ct == "text/plain" {
					m.Text += "\n" + string(data)
				}
			default:
				m.addAttachment(partName(ph.Header.Get("Content-Disposition"), ph.Header.Get("Content-Type")), ct, data)
			}
		case *mail.AttachmentHeader:
			ct, _, _ := ph.ContentType()
			if cid := strings.Trim(ph.Get("Content-Id"), "<> "); cid != "" && strings.HasPrefix(ct, "image/") {
				m.Inline[cid] = Resource{Type: ct, Data: data}
				// An embedded picture with no explicit "attachment" is part of
				// the body, not something to list as an attachment.
				if disp, _, _ := mime.ParseMediaType(ph.Get("Content-Disposition")); disp != "attachment" {
					continue
				}
			}
			name, _ := ph.Filename()
			m.addAttachment(name, ct, data)
		}
	}
	return m, nil
}

func (m *Message) addAttachment(name, ct string, data []byte) {
	if name == "" {
		name = "attachment"
		if exts, _ := mime.ExtensionsByType(ct); len(exts) > 0 {
			name += exts[0]
		}
	}
	m.Attachments = append(m.Attachments, Attachment{Index: len(m.attachData), Name: name, Type: ct, Size: len(data)})
	m.attachData = append(m.attachData, data)
}

func partName(disposition, contentType string) string {
	if _, p, err := mime.ParseMediaType(disposition); err == nil && p["filename"] != "" {
		return p["filename"]
	}
	if _, p, err := mime.ParseMediaType(contentType); err == nil {
		return p["name"]
	}
	return ""
}

func addrs(h mail.Header, key string) []Address {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		// Malformed lists are common; fall back to the raw text.
		if raw := strings.TrimSpace(h.Get(key)); raw != "" && len(list) == 0 {
			return []Address{{Addr: raw}}
		}
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, Address{Name: a.Name, Addr: a.Address})
	}
	return out
}
