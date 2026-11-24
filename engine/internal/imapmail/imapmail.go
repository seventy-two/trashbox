// Package imapmail is an IMAP account (Gmail, with an app password) as a
// mailbox.Provider.
package imapmail

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

// Mailbox is one IMAP account. It keeps one connection for commands, made
// on first use and remade after a failure; Watch opens its own.
type Mailbox struct {
	Addr     string // host:port
	Insecure bool   // plain TCP, for tests
	User     string
	Pass     string

	mu       sync.Mutex
	c        *imapclient.Client
	selected string
}

var _ mailbox.Provider = (*Mailbox)(nil)

func (b *Mailbox) dial(opts *imapclient.Options) (*imapclient.Client, error) {
	var c *imapclient.Client
	var err error
	if b.Insecure {
		c, err = imapclient.DialInsecure(b.Addr, opts)
	} else {
		c, err = imapclient.DialTLS(b.Addr, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("imap connect: %w", err)
	}
	if err := c.Login(b.User, b.Pass).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return c, nil
}

// with runs f on the command connection, with the folder selected. If the
// connection has died it reconnects and tries once more.
func (b *Mailbox) with(ctx context.Context, folder string, f func(c *imapclient.Client) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.c == nil {
			c, err := b.dial(nil)
			if err != nil {
				return err
			}
			b.c, b.selected = c, ""
		}
		err := b.selectAndRun(folder, f)
		if err == nil {
			return nil
		}
		var ie *imap.Error
		if errors.As(err, &ie) || attempt > 0 {
			return err // the server said no: reconnecting won't help
		}
		b.c.Close()
		b.c = nil
	}
}

func (b *Mailbox) selectAndRun(folder string, f func(c *imapclient.Client) error) error {
	if folder != "" && b.selected != folder {
		if _, err := b.c.Select(folder, nil).Wait(); err != nil {
			return err
		}
		b.selected = folder
	}
	return f(b.c)
}

// Folders lists every selectable mailbox with its counts.
func (b *Mailbox) Folders(ctx context.Context) ([]mailbox.Folder, error) {
	var out []mailbox.Folder
	err := b.with(ctx, "", func(c *imapclient.Client) error {
		list, err := c.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, l := range list {
			names[l.Mailbox] = true
		}
		out = out[:0]
		for _, l := range list {
			if slices.Contains(l.Attrs, imap.MailboxAttrNoSelect) || slices.Contains(l.Attrs, imap.MailboxAttrNonExistent) {
				continue
			}
			f := mailbox.Folder{ID: l.Mailbox, Name: l.Mailbox, Role: role(l)}
			if l.Delim != 0 {
				if i := strings.LastIndexByte(l.Mailbox, byte(l.Delim)); i >= 0 {
					f.Name = l.Mailbox[i+1:]
					if p := l.Mailbox[:i]; names[p] {
						f.Parent = p
					}
				}
			}
			st, err := c.Status(l.Mailbox, &imap.StatusOptions{NumMessages: true, NumUnseen: true}).Wait()
			if err == nil {
				if st.NumMessages != nil {
					f.Total = int(*st.NumMessages)
				}
				if st.NumUnseen != nil {
					f.Unread = int(*st.NumUnseen)
				}
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

func role(l *imap.ListData) string {
	if strings.EqualFold(l.Mailbox, "INBOX") {
		return mailbox.RoleInbox
	}
	for _, a := range l.Attrs {
		switch a {
		case imap.MailboxAttrSent:
			return mailbox.RoleSent
		case imap.MailboxAttrDrafts:
			return mailbox.RoleDrafts
		case imap.MailboxAttrTrash:
			return mailbox.RoleTrash
		case imap.MailboxAttrJunk:
			return mailbox.RoleJunk
		case imap.MailboxAttrArchive:
			return mailbox.RoleArchive
		case imap.MailboxAttrAll:
			return mailbox.RoleAll
		case imap.MailboxAttrFlagged:
			return mailbox.RoleFlagged
		}
	}
	return ""
}

const batch = 200

var headerFetch = &imap.FetchOptions{UID: true, Envelope: true, Flags: true, InternalDate: true, BodyStructure: &imap.FetchItemBodyStructure{}}

func header(m *imapclient.FetchMessageBuffer) mailbox.Header {
	h := mailbox.Header{RID: strconv.FormatUint(uint64(m.UID), 10), Date: m.InternalDate}
	h.Seen = slices.Contains(m.Flags, imap.FlagSeen)
	h.Flagged = slices.Contains(m.Flags, imap.FlagFlagged)
	if e := m.Envelope; e != nil {
		h.Subject = e.Subject
		if len(e.From) > 0 {
			h.FromName, h.FromAddr = e.From[0].Name, strings.ToLower(e.From[0].Addr())
		}
		to := make([]string, 0, len(e.To))
		for _, a := range e.To {
			if a.Name != "" {
				to = append(to, a.Name)
			} else {
				to = append(to, a.Addr())
			}
		}
		h.To = strings.Join(to, ", ")
		if h.Date.IsZero() {
			h.Date = e.Date
		}
	}
	if m.BodyStructure != nil {
		m.BodyStructure.Walk(func(_ []int, p imap.BodyStructure) bool {
			if d := p.Disposition(); d != nil && strings.EqualFold(d.Value, "attachment") {
				h.Attach = true
			}
			return !h.Attach
		})
	}
	return h
}

// fetchHeaders fetches headers for uids, a batch at a time.
func fetchHeaders(c *imapclient.Client, uids []imap.UID) ([]mailbox.Header, error) {
	var out []mailbox.Header
	for i := 0; i < len(uids); i += batch {
		set := imap.UIDSetNum(uids[i:min(i+batch, len(uids))]...)
		msgs, err := c.Fetch(set, headerFetch).Collect()
		if err != nil {
			return out, err
		}
		for _, m := range msgs {
			out = append(out, header(m))
		}
	}
	return out, nil
}

func parseUID(rid string) (imap.UID, bool) {
	n, err := strconv.ParseUint(rid, 10, 32)
	return imap.UID(n), err == nil && n > 0
}

// edge keeps messages near the window's start out of "removed": IMAP's
// SINCE works on whole days in the server's time zone.
const edge = 48 * time.Hour

// Sync compares the server's UIDs in the window with what the cache knows:
// new UIDs are fetched in full, known ones get their flags refreshed, and
// missing ones are removed. state is the folder's UIDVALIDITY.
func (b *Mailbox) Sync(ctx context.Context, folder, state string, since time.Time, known map[string]time.Time) (mailbox.Delta, error) {
	var d mailbox.Delta
	err := b.with(ctx, "", func(c *imapclient.Client) error {
		sel, err := c.Select(folder, nil).Wait()
		if err != nil {
			return err
		}
		b.selected = folder
		d.State = strconv.FormatUint(uint64(sel.UIDValidity), 10)
		d.Reset = state != d.State
		if d.Reset {
			known = nil
		}
		res, err := c.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
		if err != nil {
			return err
		}
		present := res.AllUIDs()
		var fresh, old []imap.UID
		seen := make(map[string]bool, len(present))
		for _, u := range present {
			rid := strconv.FormatUint(uint64(u), 10)
			seen[rid] = true
			if _, ok := known[rid]; ok {
				old = append(old, u)
			} else {
				fresh = append(fresh, u)
			}
		}
		if d.New, err = fetchHeaders(c, fresh); err != nil {
			return err
		}
		for i := 0; i < len(old); i += batch {
			set := imap.UIDSetNum(old[i:min(i+batch, len(old))]...)
			msgs, err := c.Fetch(set, &imap.FetchOptions{UID: true, Flags: true}).Collect()
			if err != nil {
				return err
			}
			for _, m := range msgs {
				d.Flags = append(d.Flags, mailbox.Flags{
					RID: strconv.FormatUint(uint64(m.UID), 10), Seen: slices.Contains(m.Flags, imap.FlagSeen),
					Flagged: slices.Contains(m.Flags, imap.FlagFlagged),
				})
			}
		}
		for rid, date := range known {
			if !seen[rid] && date.After(since.Add(edge)) {
				d.Removed = append(d.Removed, rid)
			}
		}
		return nil
	})
	return d, err
}

// Older returns up to n messages that arrived before `before`. UIDs rise
// with arrival, so the newest candidates are the highest UIDs.
func (b *Mailbox) Older(ctx context.Context, folder string, before time.Time, n int) ([]mailbox.Header, bool, error) {
	var out []mailbox.Header
	more := false
	err := b.with(ctx, folder, func(c *imapclient.Client) error {
		// BEFORE is whole days, so search a day past and filter exactly.
		res, err := c.UIDSearch(&imap.SearchCriteria{Before: before.AddDate(0, 0, 1)}, nil).Wait()
		if err != nil {
			return err
		}
		uids := res.AllUIDs()
		slices.Sort(uids)
		take := min(len(uids), n+50)
		hs, err := fetchHeaders(c, uids[len(uids)-take:])
		if err != nil {
			return err
		}
		slices.SortFunc(hs, func(a, b mailbox.Header) int { return b.Date.Compare(a.Date) })
		for _, h := range hs {
			if h.Date.Before(before) {
				out = append(out, h)
			}
		}
		more = len(uids) > take || len(out) > n
		if len(out) > n {
			out = out[:n]
		}
		return nil
	})
	return out, more, err
}

// Raw fetches the message source with BODY.PEEK, so it stays unread.
func (b *Mailbox) Raw(ctx context.Context, folder, rid string) ([]byte, error) {
	uid, ok := parseUID(rid)
	if !ok {
		return nil, fmt.Errorf("bad uid %q", rid)
	}
	var raw []byte
	err := b.with(ctx, folder, func(c *imapclient.Client) error {
		msgs, err := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		}).Collect()
		if err != nil {
			return err
		}
		if len(msgs) == 0 || len(msgs[0].BodySection) == 0 {
			return fmt.Errorf("message %s is gone", rid)
		}
		raw = msgs[0].BodySection[0].Bytes
		return nil
	})
	return raw, err
}

// SetSeen adds or removes \Seen.
func (b *Mailbox) SetSeen(ctx context.Context, folder string, rids []string, seen bool) error {
	var set imap.UIDSet
	for _, r := range rids {
		if u, ok := parseUID(r); ok {
			set.AddNum(u)
		}
	}
	if len(set) == 0 {
		return nil
	}
	op := imap.StoreFlagsAdd
	if !seen {
		op = imap.StoreFlagsDel
	}
	return b.with(ctx, folder, func(c *imapclient.Client) error {
		return c.Store(set, &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	})
}

// Watch IDLEs on its own connection, calling changed on any news about the
// folder. It returns when ctx ends or the connection drops (the caller
// waits a moment and calls it again).
func (b *Mailbox) Watch(ctx context.Context, folder string, changed func()) error {
	c, err := b.dial(&imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{
		Expunge: func(uint32) { changed() },
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages != nil {
				changed()
			}
		},
		Fetch: func(*imapclient.FetchMessageData) { changed() },
	}})
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return err
	}
	idle, err := c.Idle()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		idle.Close()
		idle.Wait()
		c.Logout().Wait()
		return ctx.Err()
	case <-c.Closed():
		return errors.New("imap idle: connection closed")
	}
}

// Close logs out the command connection.
func (b *Mailbox) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c == nil {
		return nil
	}
	b.c.Logout().Wait()
	err := b.c.Close()
	b.c = nil
	return err
}
