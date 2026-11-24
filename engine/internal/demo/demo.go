// Package demo is a made-up pair of accounts for screenshots and testing
// the window: invented people and companies only, never real mail.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/compose"
	"github.com/seventy-two/trashbox/engine/internal/engine"
	"github.com/seventy-two/trashbox/engine/internal/mailbox"
	"github.com/seventy-two/trashbox/engine/internal/mailparse"
)

type msg struct {
	h    mailbox.Header
	html string
	text string
	cc   string
	raw  []byte // as sent, for messages sent from the demo
}

// Provider is one demo account.
type Provider struct {
	id      string // the account's address
	codes   bool   // gets the TRASHBOX_DEMO_CODE email
	mu      sync.Mutex
	folders []mailbox.Folder
	msgs    map[string][]*msg // folder → messages
}

var _ mailbox.Provider = (*Provider)(nil)

// account is one demo account's mail.
type account struct {
	id, kind string
	inbox    []seed
	junk     []seed
	codes    bool
}

func build(now time.Time, a account) *Provider {
	p := &Provider{id: a.id, msgs: map[string][]*msg{}, codes: a.codes}
	p.folders = []mailbox.Folder{
		{ID: "inbox", Name: "Inbox", Role: mailbox.RoleInbox},
		{ID: "sent", Name: "Sent", Role: mailbox.RoleSent},
		{ID: "archive", Name: "Archive", Role: mailbox.RoleArchive},
		{ID: "receipts", Name: "Receipts", Parent: "archive"},
		{ID: "junk", Name: "Junk", Role: mailbox.RoleJunk},
		{ID: "trash", Name: "Deleted", Role: mailbox.RoleTrash},
	}
	n := 0
	add := func(folder string, s seed) {
		n++
		m := &msg{h: mailbox.Header{
			RID: fmt.Sprintf("%s-%d", folder, n), Date: now.Add(-s.ago).Truncate(time.Second),
			FromName: s.name, FromAddr: s.addr, To: "Me", Subject: s.subject,
			Seen: s.seen, Flagged: s.flagged, Attach: s.attach,
		}, html: s.html, text: s.text, cc: s.cc}
		p.msgs[folder] = append(p.msgs[folder], m)
	}
	for _, s := range a.inbox {
		add("inbox", s)
	}
	for _, s := range a.junk {
		add("junk", s)
	}
	add("sent", seed{ago: 30 * time.Minute, name: "Me", addr: a.id, subject: "Re: Sunday roast?", seen: true, text: "Yes! 1pm is perfect."})
	add("receipts", seed{ago: 40 * 24 * time.Hour, name: "Kettle & Bean", addr: "orders@kettlebean.example", subject: "Order KB-40017", seen: true, html: receipt})
	p.recount()
	return p
}

// Accounts returns the demo accounts. TRASHBOX_DEMO_IMG replaces the
// allotment newsletter's picture, so a test can watch a server for the
// request.
func Accounts() []engine.Account {
	now := time.Now()
	if img := os.Getenv("TRASHBOX_DEMO_IMG"); img != "" {
		allotmentImg = img
	}
	var out []engine.Account
	for _, a := range accounts() {
		p := build(now, a)
		out = append(out, engine.Account{ID: a.id, Kind: a.kind, Provider: p, Send: p.Send})
	}
	return out
}

func (p *Provider) recount() {
	for i, f := range p.folders {
		p.folders[i].Total = len(p.msgs[f.ID])
		p.folders[i].Unread = 0
		for _, m := range p.msgs[f.ID] {
			if !m.h.Seen {
				p.folders[i].Unread++
			}
		}
	}
}

// Folders lists the folders.
func (p *Provider) Folders(context.Context) ([]mailbox.Folder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recount()
	return append([]mailbox.Folder(nil), p.folders...), nil
}

// Sync returns the whole window each time (it's tiny).
func (p *Provider) Sync(_ context.Context, folder, _ string, since time.Time, _ map[string]time.Time) (mailbox.Delta, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := mailbox.Delta{Reset: true, State: "demo"}
	for _, m := range p.msgs[folder] {
		if !m.h.Date.Before(since) {
			d.New = append(d.New, m.h)
		}
	}
	return d, nil
}

// Older returns messages from before the window.
func (p *Provider) Older(_ context.Context, folder string, before time.Time, n int) ([]mailbox.Header, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var hs []mailbox.Header
	for _, m := range p.msgs[folder] {
		if m.h.Date.Before(before) {
			hs = append(hs, m.h)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Date.After(hs[j].Date) })
	if len(hs) > n {
		return hs[:n], true, nil
	}
	return hs, false, nil
}

// Raw builds the message source.
func (p *Provider) Raw(_ context.Context, folder, rid string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.msgs[folder] {
		if m.h.RID != rid {
			continue
		}
		if m.raw != nil {
			return m.raw, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "From: %q <%s>\r\nTo: Me <%s>\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@demo.example>\r\nMIME-Version: 1.0\r\n",
			m.h.FromName, m.h.FromAddr, p.id, m.h.Subject, m.h.Date.Format(time.RFC1123Z), m.h.RID)
		if m.cc != "" {
			fmt.Fprintf(&b, "Cc: %s\r\n", m.cc)
		}
		body, ct := m.text, "text/plain"
		if m.html != "" {
			body, ct = m.html, "text/html"
		}
		if !m.h.Attach {
			fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\n\r\n%s\r\n", ct, body)
			return []byte(b.String()), nil
		}
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: %s; charset=utf-8\r\n\r\n%s\r\n", ct, body)
		b.WriteString("--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"receipt.pdf\"\r\n\r\n%PDF-1.4 demo\r\n--b--\r\n")
		return []byte(b.String()), nil
	}
	return nil, fmt.Errorf("no message %s", rid)
}

// SetSeen marks messages read or unread.
func (p *Provider) SetSeen(_ context.Context, folder string, rids []string, seen bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.msgs[folder] {
		for _, r := range rids {
			if m.h.RID == r {
				m.h.Seen = seen
			}
		}
	}
	return nil
}

// Watch: the demo has no push, except that with TRASHBOX_DEMO_CODE set the
// first account gets a sign-in code email a few seconds in, to test codes.
func (p *Provider) Watch(ctx context.Context, folder string, changed func()) error {
	if os.Getenv("TRASHBOX_DEMO_CODE") == "" || !p.codes {
		return mailbox.ErrNoPush
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	p.mu.Lock()
	p.msgs[folder] = append(p.msgs[folder], &msg{h: mailbox.Header{
		RID: "code-1", Date: time.Now().Truncate(time.Second), FromName: "Fernhill Bank",
		FromAddr: "no-reply@fernhill.example", To: "Me", Subject: "Your sign-in code",
	}, text: "Your Fernhill sign-in code is 739104.\n\nIt expires in 10 minutes."})
	p.mu.Unlock()
	changed()
	<-ctx.Done()
	return ctx.Err()
}

// Send files the message in Sent, as Gmail and Outlook do. Nothing leaves
// the machine. TRASHBOX_DEMO_SENDFAIL makes it fail, to test the window.
func (p *Provider) Send(_ context.Context, o compose.Outgoing) error {
	if os.Getenv("TRASHBOX_DEMO_SENDFAIL") != "" {
		return errors.New("the demo server said no")
	}
	m, err := mailparse.Parse(o.Raw)
	if err != nil {
		return err
	}
	to := make([]string, 0, len(m.To))
	for _, a := range m.To {
		if a.Name != "" {
			to = append(to, a.Name)
		} else {
			to = append(to, a.Addr)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs["sent"] = append(p.msgs["sent"], &msg{h: mailbox.Header{
		RID: fmt.Sprintf("sent-%d", time.Now().UnixNano()), Date: time.Now().Truncate(time.Second),
		FromName: "Me", FromAddr: p.id, To: strings.Join(to, ", "), Subject: m.Subject, Seen: true,
	}, raw: o.Raw})
	return nil
}

// Close does nothing.
func (p *Provider) Close() error { return nil }
