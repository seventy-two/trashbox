package cache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

func open(t *testing.T) *Cache {
	c, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func h(rid string, date time.Time, subject string) mailbox.Header {
	return mailbox.Header{RID: rid, Date: date, Subject: subject, FromName: "Ann"}
}

var base = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestFoldersKeepStateAndDropMissing(t *testing.T) {
	c := open(t)
	c.SaveFolders("a", []mailbox.Folder{{ID: "IN", Name: "Inbox", Role: "inbox"}, {ID: "OLD", Name: "Old"}})
	c.SetSync("a", "IN", "s1", base)
	c.Apply("a", "OLD", base, mailbox.Delta{New: []mailbox.Header{h("1", base, "x")}})

	c.SaveFolders("a", []mailbox.Folder{{ID: "IN", Name: "Inbox", Role: "inbox", Unread: 5}})
	fs, err := c.Folders("a")
	if err != nil || len(fs) != 1 || fs[0].State != "s1" || !fs[0].Since.Equal(base) || fs[0].Unread != 5 {
		t.Fatalf("%+v %v", fs, err)
	}
	if ms, _ := c.List(Query{Account: "a", Folder: "OLD"}); len(ms) != 0 {
		t.Fatalf("messages of a deleted folder should go: %+v", ms)
	}
}

func TestApplyAndList(t *testing.T) {
	c := open(t)
	c.SaveFolders("a", []mailbox.Folder{{ID: "IN", Role: "inbox", Name: "Inbox", Unread: 2}, {ID: "X", Name: "Other"}})
	c.SaveFolders("b", []mailbox.Folder{{ID: "INBOX", Role: "inbox", Name: "INBOX"}})
	since := base.AddDate(0, 0, -60)

	added, err := c.Apply("a", "IN", since, mailbox.Delta{Reset: true, New: []mailbox.Header{
		h("1", base, "one"), h("2", base.Add(time.Hour), "two"), h("3", base.Add(2*time.Hour), "three"),
	}})
	if err != nil || len(added) != 3 {
		t.Fatalf("added %v %v", added, err)
	}
	c.Apply("b", "INBOX", since, mailbox.Delta{New: []mailbox.Header{h("9", base.Add(90*time.Minute), "bee")}})
	c.Apply("a", "X", since, mailbox.Delta{New: []mailbox.Header{h("7", base.Add(5*time.Hour), "not inbox")}})

	// Second sync: one changed in full (not "added"), a flag change, a removal.
	added, err = c.Apply("a", "IN", since, mailbox.Delta{
		New:     []mailbox.Header{h("3", base.Add(2*time.Hour), "three!"), h("4", base.Add(3*time.Hour), "four")},
		Flags:   []mailbox.Flags{{RID: "2", Seen: true}},
		Removed: []string{"1"},
	})
	if err != nil || len(added) != 1 {
		t.Fatalf("added %v %v", added, err)
	}

	all, _ := c.List(Query{Unified: true})
	var subs []string
	for _, m := range all {
		subs = append(subs, m.Subject)
	}
	if want := "[four three! bee two]"; fmtS(subs) != want {
		t.Fatalf("unified %v, want %s", subs, want)
	}
	if !all[3].Seen {
		t.Error("flag not applied")
	}

	// Paging with the cursor.
	page1, _ := c.List(Query{Unified: true, Limit: 2})
	last := page1[1]
	page2, _ := c.List(Query{Unified: true, Limit: 2, BeforeDate: last.Date, BeforeID: last.ID})
	if len(page2) != 2 || page2[0].Subject != "bee" {
		t.Fatalf("page2 %+v", page2)
	}

	// Reset drops what the server no longer has in the window, but keeps
	// older mail fetched from before the window.
	c.AddOlder("a", "IN", []mailbox.Header{h("0", since.AddDate(0, 0, -5), "ancient")})
	c.Apply("a", "IN", since, mailbox.Delta{Reset: true, New: []mailbox.Header{h("4", base.Add(3*time.Hour), "four")}})
	ms, _ := c.List(Query{Account: "a", Folder: "IN"})
	if len(ms) != 2 || ms[0].Subject != "four" || ms[1].Subject != "ancient" {
		t.Fatalf("after reset %+v", ms)
	}
	if o, ok, _ := c.Oldest("a", "IN"); !ok || !o.Equal(since.AddDate(0, 0, -5)) {
		t.Fatalf("oldest %v", o)
	}
	known, _ := c.Known("a", "IN", since)
	if len(known) != 1 {
		t.Fatalf("known %v", known)
	}
}

func TestSeenAdjustsUnreadAndBodies(t *testing.T) {
	c := open(t)
	c.SaveFolders("a", []mailbox.Folder{{ID: "IN", Role: "inbox", Name: "Inbox", Unread: 2}})
	added, _ := c.Apply("a", "IN", base, mailbox.Delta{New: []mailbox.Header{h("1", base, "x"), h("2", base, "y")}})
	if err := c.SetSeen(added, true); err != nil {
		t.Fatal(err)
	}
	c.SetSeen(added[:1], true) // already read: no double count
	if f, _ := c.Folder("a", "IN"); f.Unread != 0 {
		t.Fatalf("unread %d", f.Unread)
	}
	m, _ := c.Get(added[0])
	if !m.Seen {
		t.Fatal("not seen")
	}

	if _, ok, _ := c.Body(added[0]); ok {
		t.Fatal("no body yet")
	}
	c.SaveBody(added[0], []byte("raw"), "preview")
	raw, ok, err := c.Body(added[0])
	if !ok || err != nil || string(raw) != "raw" {
		t.Fatalf("body %q %v %v", raw, ok, err)
	}
	if m, _ := c.Get(added[0]); m.Snippet != "preview" {
		t.Fatalf("snippet %q", m.Snippet)
	}
	// Bodies go with their message.
	c.Apply("a", "IN", base, mailbox.Delta{Removed: []string{"1"}})
	if _, ok, _ := c.Body(added[0]); ok {
		t.Fatal("body should be deleted with its message")
	}
}

func fmtS(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}
