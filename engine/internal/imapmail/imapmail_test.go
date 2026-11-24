package imapmail

import (
	"context"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

const user, pass = "me@gmail.com", "pw"

type server struct {
	t    *testing.T
	addr string
}

func newServer(t *testing.T) *server {
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(user, pass)
	u.Create("INBOX", nil)
	u.Create("Work", nil)
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &server{t, ln.Addr().String()}
}

// admin runs f on a separate client, as another device changing the mailbox.
func (s *server) admin(f func(c *imapclient.Client)) {
	c, err := imapclient.DialInsecure(s.addr, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(user, pass).Wait(); err != nil {
		s.t.Fatal(err)
	}
	f(c)
}

func (s *server) add(folder, subject string, at time.Time, flags ...imap.Flag) {
	s.admin(func(c *imapclient.Client) {
		msg := fmt.Sprintf("From: Ann <Ann@Example.com>\r\nTo: me@gmail.com\r\nSubject: %s\r\n\r\nbody of %s\r\n", subject, subject)
		cmd := c.Append(folder, int64(len(msg)), &imap.AppendOptions{Time: at, Flags: flags})
		cmd.Write([]byte(msg))
		cmd.Close()
		if _, err := cmd.Wait(); err != nil {
			s.t.Fatal(err)
		}
	})
}

func subjects(hs []mailbox.Header) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Subject)
	}
	slices.Sort(out)
	return out
}

func TestSyncCycle(t *testing.T) {
	s := newServer(t)
	now := time.Now()
	s.add("INBOX", "ancient", now.AddDate(0, -6, 0))
	s.add("INBOX", "recent1", now.Add(-24*time.Hour*10), imap.FlagSeen)
	s.add("INBOX", "recent2", now.Add(-time.Hour))

	b := &Mailbox{Addr: s.addr, Insecure: true, User: user, Pass: pass}
	defer b.Close()
	ctx := context.Background()
	since := now.AddDate(0, 0, -60)

	fs, err := b.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Role != mailbox.RoleInbox || fs[0].Total != 3 || fs[0].Unread != 2 {
		t.Fatalf("folders %+v", fs)
	}

	d, err := b.Sync(ctx, "INBOX", "", since, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Reset || d.State == "" || !slices.Equal(subjects(d.New), []string{"recent1", "recent2"}) {
		t.Fatalf("first sync %+v", d)
	}
	known := map[string]time.Time{}
	for _, h := range d.New {
		known[h.RID] = h.Date
		if h.Subject == "recent1" && (!h.Seen || h.FromName != "Ann" || h.FromAddr != "ann@example.com") {
			t.Errorf("header %+v", h)
		}
	}

	// Another device: a new message, one marked read, one deleted.
	s.add("INBOX", "recent3", now)
	s.admin(func(c *imapclient.Client) {
		c.Select("INBOX", nil).Wait()
		c.Store(imap.SeqSetNum(3), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
		c.Store(imap.SeqSetNum(2), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		c.Expunge().Close()
	})

	d2, err := b.Sync(ctx, "INBOX", d.State, since, known)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Reset || !slices.Equal(subjects(d2.New), []string{"recent3"}) {
		t.Fatalf("second sync new %+v", d2)
	}
	if len(d2.Removed) != 1 || len(d2.Flags) != 1 || !d2.Flags[0].Seen {
		t.Fatalf("second sync removed %v flags %+v", d2.Removed, d2.Flags)
	}

	older, more, err := b.Older(ctx, "INBOX", since, 10)
	if err != nil || more || len(older) != 1 || older[0].Subject != "ancient" {
		t.Fatalf("older %+v %v %v", older, more, err)
	}

	// Raw must not mark the message read; SetSeen must.
	rid := d2.New[0].RID
	raw, err := b.Raw(ctx, "INBOX", rid)
	if err != nil || len(raw) == 0 {
		t.Fatalf("raw %q %v", raw, err)
	}
	unread := func() int {
		fs, _ := b.Folders(ctx)
		return fs[0].Unread
	}
	if unread() != 2 {
		t.Fatalf("raw marked read: unread %d", unread())
	}
	if err := b.SetSeen(ctx, "INBOX", []string{rid}, true); err != nil {
		t.Fatal(err)
	}
	if unread() != 1 {
		t.Fatalf("SetSeen: unread %d", unread())
	}
}

func TestWatchSeesNewMail(t *testing.T) {
	s := newServer(t)
	b := &Mailbox{Addr: s.addr, Insecure: true, User: user, Pass: pass}
	ctx, cancel := context.WithCancel(context.Background())
	changed := make(chan struct{}, 10)
	done := make(chan error, 1)
	go func() { done <- b.Watch(ctx, "INBOX", func() { changed <- struct{}{} }) }()

	time.Sleep(200 * time.Millisecond) // let IDLE start
	s.add("INBOX", "ping", time.Now())
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch didn't stop")
	}
}

func TestReconnectsAfterDrop(t *testing.T) {
	s := newServer(t)
	s.add("INBOX", "x", time.Now())
	b := &Mailbox{Addr: s.addr, Insecure: true, User: user, Pass: pass}
	ctx := context.Background()
	if _, err := b.Folders(ctx); err != nil {
		t.Fatal(err)
	}
	b.c.Close() // the network drops
	if _, err := b.Folders(ctx); err != nil {
		t.Fatalf("should reconnect: %v", err)
	}
}

func TestSearch(t *testing.T) {
	s := newServer(t)
	now := time.Now().Truncate(time.Second)
	s.add("INBOX", "Sunday roast", now.AddDate(-2, 0, 0))
	s.add("INBOX", "Weekday lunch", now.AddDate(-1, 0, 0))
	s.add("INBOX", "Another roast", now)
	b := &Mailbox{Addr: s.addr, Insecure: true, User: user, Pass: pass}
	defer b.Close()
	got, err := b.Search(context.Background(), "INBOX", "roast", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Subject != "Another roast" || got[0].Folder != "INBOX" {
		t.Fatalf("search %+v", got)
	}
	// Every word must match; the newest n win.
	if got, _ := b.Search(context.Background(), "INBOX", "sunday roast", 10); len(got) != 1 {
		t.Errorf("two words: %+v", got)
	}
	if got, _ := b.Search(context.Background(), "INBOX", "roast", 1); len(got) != 1 || got[0].Subject != "Another roast" {
		t.Errorf("newest one: %+v", got)
	}
}
