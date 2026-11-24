// Package demo is a made-up pair of accounts for screenshots and testing
// the window: invented people and companies only, never real mail.
package demo

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/engine"
	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

type msg struct {
	h    mailbox.Header
	html string
	text string
}

// Provider is one demo account.
type Provider struct {
	codes   bool // gets the TRASHBOX_DEMO_CODE email
	mu      sync.Mutex
	folders []mailbox.Folder
	msgs    map[string][]*msg // folder → messages
}

var _ mailbox.Provider = (*Provider)(nil)

type seed struct {
	ago      time.Duration
	name     string
	addr     string
	subject  string
	text     string
	html     string
	seen     bool
	flagged  bool
	attach   bool
	folderID string
}

const newsletter = `<html><body style="margin:0;background:#f3efe6;font-family:Georgia,serif">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table width="560" cellpadding="24" style="background:#fffdf8">
<tr><td><img src="https://images.example.com/allotment/header.jpg" width="512" alt="Allotment Weekly"></td></tr>
<tr><td><h1 style="color:#4a5a2a;font-weight:normal">Leeks, at last</h1>
<p style="color:#333;line-height:1.6">The frost held off long enough this week for the leeks to thicken up nicely.
Plot 14 is still winning the marrow argument, and the shed roof has been patched (again).</p>
<p style="color:#333;line-height:1.6">This Saturday: seed swap at the gate from 10 until the tea runs out.</p>
<p><a href="https://allotment.example.com/swap" style="background:#4a5a2a;color:#fff;padding:10px 18px;text-decoration:none">See what's on the table</a></p>
</td></tr></table></td></tr></table></body></html>`

const receipt = `<html><body style="font-family:Arial,sans-serif;color:#222">
<h2 style="margin:0 0 12px">Thanks for your order</h2>
<p>Order <b>KB-48213</b> is on its way.</p>
<table cellpadding="6" style="border-collapse:collapse">
<tr style="background:#eee"><th align="left">Item</th><th align="right">Price</th></tr>
<tr><td>Kettle, brushed steel</td><td align="right">£34.00</td></tr>
<tr><td>Descaler (2 pack)</td><td align="right">£4.50</td></tr>
<tr><td><b>Total</b></td><td align="right"><b>£38.50</b></td></tr></table>
<p style="color:#666;font-size:12px">Kettle &amp; Bean Ltd · this is an invented shop</p></body></html>`

var seeds = map[string][]seed{
	"inbox": {
		{ago: 4 * time.Minute, name: "Fernhill Bank", addr: "no-reply@fernhill.example", subject: "Your sign-in code",
			text: "Your Fernhill sign-in code is 482913.\n\nIt expires in 10 minutes. If this wasn't you, you can ignore this email."},
		{ago: 40 * time.Minute, name: "Priya Shah", addr: "priya@example.org", subject: "Sunday roast?",
			text: "Are you still up for Sunday? I'm thinking the place by the river, about 1pm.\n\nSam's coming too, and might bring the dog.\n\nP"},
		{ago: 3 * time.Hour, name: "Allotment Weekly", addr: "news@allotment.example.com", subject: "Leeks, at last 🌱", html: newsletter},
		{ago: 7 * time.Hour, name: "Kettle & Bean", addr: "orders@kettlebean.example", subject: "Order KB-48213 is on its way", html: receipt, seen: true, attach: true},
		{ago: 26 * time.Hour, name: "Tom Okafor", addr: "tom.okafor@example.net", subject: "Re: bike lights", seen: true, flagged: true,
			text: "Found them. They were in the hall cupboard behind the hoover, obviously.\n\nI'll drop them round on Thursday.\n\n> Did you ever find the bike lights?\n> I can't find mine anywhere."},
		{ago: 50 * time.Hour, name: "Harbourside Cinema", addr: "tickets@harbourside.example", subject: "Your tickets for Friday", seen: true,
			html: `<div style="font-family:Segoe UI,sans-serif"><h2>Friday, 19:40, Screen 3</h2><p>Seats <b>F7, F8</b>. Show this email at the door.</p></div>`},
		{ago: 5 * 24 * time.Hour, name: "Council Tax", addr: "billing@council.example.gov", subject: "Your new bill", seen: true,
			text: "Your bill for next year is ready to view online.\n\nhttps://council.example.gov/bills"},
	},
	"older": {
		{ago: 80 * 24 * time.Hour, name: "Priya Shah", addr: "priya@example.org", subject: "Photos from the walk", seen: true, attach: true,
			text: "A few from the top of the hill. The fog did lift in the end!"},
		{ago: 95 * 24 * time.Hour, name: "Railcard", addr: "renewals@railcard.example", subject: "Time to renew", seen: true,
			text: "Your railcard runs out next month."},
	},
}

var seeds2 = []seed{
	{ago: 20 * time.Minute, name: "Climbing Club", addr: "hello@climbing.example", subject: "Wednesday session moved to 7pm",
		text: "The wall's being reset, so we're starting an hour later this week."},
	{ago: 9 * time.Hour, name: "Mum", addr: "mum@example.com", subject: "Christmas", seen: true,
		text: "Let me know about Christmas soon please! Dad wants to book the train."},
	{ago: 4 * 24 * time.Hour, name: "Bookshop on the Corner", addr: "shop@books.example", subject: "Your reservation is in", seen: true,
		text: "The book you reserved has arrived. We'll keep it behind the counter for two weeks."},
}

func build(now time.Time, inbox []seed, older []seed) *Provider {
	p := &Provider{msgs: map[string][]*msg{}}
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
		}, html: s.html, text: s.text}
		p.msgs[folder] = append(p.msgs[folder], m)
	}
	for _, s := range inbox {
		add("inbox", s)
	}
	for _, s := range older {
		add("inbox", s)
	}
	add("sent", seed{ago: 30 * time.Minute, name: "Me", addr: "me@example.com", subject: "Re: Sunday roast?", seen: true, text: "Yes! 1pm is perfect."})
	add("junk", seed{ago: 2 * time.Hour, name: "Prize Desk", addr: "winner@prizes.example", subject: "You've won (again)", text: "Claim now!"})
	add("receipts", seed{ago: 40 * 24 * time.Hour, name: "Kettle & Bean", addr: "orders@kettlebean.example", subject: "Order KB-40017", seen: true, html: receipt})
	p.recount()
	return p
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

// Accounts returns the two demo accounts. TRASHBOX_DEMO_IMG replaces the
// newsletter's picture, so a test can watch a server for the request.
func Accounts() []engine.Account {
	now := time.Now()
	if img := os.Getenv("TRASHBOX_DEMO_IMG"); img != "" {
		seeds["inbox"][2].html = strings.Replace(newsletter, "https://images.example.com/allotment/header.jpg", img, 1)
	}
	return []engine.Account{
		{ID: "me@example.com", Kind: "outlook", Provider: withCodes(build(now, seeds["inbox"], seeds["older"]))},
		{ID: "me.too@example.org", Kind: "gmail", Provider: build(now, seeds2, nil)},
	}
}

func withCodes(p *Provider) *Provider {
	p.codes = true
	return p
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
		var b strings.Builder
		fmt.Fprintf(&b, "From: %q <%s>\r\nTo: Me <me@example.com>\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
			m.h.FromName, m.h.FromAddr, m.h.Subject, m.h.Date.Format(time.RFC1123Z))
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

// Close does nothing.
func (p *Provider) Close() error { return nil }
