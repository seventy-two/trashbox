package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/cache"
	"github.com/seventy-two/trashbox/engine/internal/compose"
	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

// fake is an in-memory account with one inbox.
type fake struct {
	mu     sync.Mutex
	msgs   map[string]mailbox.Header
	push   chan struct{} // nil: no push
	seenOK []string
	raws   int
}

func newFake(push bool) *fake {
	f := &fake{msgs: map[string]mailbox.Header{}}
	if push {
		f.push = make(chan struct{}, 10)
	}
	return f
}

func (f *fake) add(rid, subject string, date time.Time) {
	f.mu.Lock()
	f.msgs[rid] = mailbox.Header{RID: rid, Subject: subject, Date: date, FromName: "Ann"}
	f.mu.Unlock()
	if f.push != nil {
		f.push <- struct{}{}
	}
}

func (f *fake) Folders(context.Context) ([]mailbox.Folder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	unread := 0
	for _, m := range f.msgs {
		if !m.Seen {
			unread++
		}
	}
	return []mailbox.Folder{{ID: "IN", Name: "Inbox", Role: mailbox.RoleInbox, Unread: unread, Total: len(f.msgs)}}, nil
}

func (f *fake) Sync(_ context.Context, _ string, state string, since time.Time, known map[string]time.Time) (mailbox.Delta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := mailbox.Delta{State: "s", Reset: state == ""}
	for rid, m := range f.msgs {
		if m.Date.Before(since) {
			continue
		}
		if _, ok := known[rid]; ok && !d.Reset {
			d.Flags = append(d.Flags, mailbox.Flags{RID: rid, Seen: m.Seen})
		} else {
			d.New = append(d.New, m)
		}
	}
	for rid := range known {
		if _, ok := f.msgs[rid]; !ok {
			d.Removed = append(d.Removed, rid)
		}
	}
	return d, nil
}

func (f *fake) Older(_ context.Context, _ string, before time.Time, n int) ([]mailbox.Header, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var hs []mailbox.Header
	for _, m := range f.msgs {
		if m.Date.Before(before) {
			hs = append(hs, m)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Date.After(hs[j].Date) })
	if len(hs) > n {
		return hs[:n], true, nil
	}
	return hs, false, nil
}

func (f *fake) Raw(_ context.Context, _, rid string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raws++
	m := f.msgs[rid]
	body := "<p>Hello <b>there</b></p>"
	if strings.Contains(m.Subject, "code") {
		body = "<p>Your sign-in code is <b>482913</b>.</p><p>It expires in 10 minutes.</p>"
	}
	return []byte("From: Ann <ann@x.com>\r\nSubject: " + m.Subject + "\r\nContent-Type: text/html\r\n\r\n" + body), nil
}

func (f *fake) SetSeen(_ context.Context, _ string, rids []string, seen bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rids {
		m := f.msgs[r]
		m.Seen = seen
		f.msgs[r] = m
		f.seenOK = append(f.seenOK, r)
	}
	return nil
}

func (f *fake) Watch(ctx context.Context, _ string, changed func()) error {
	if f.push == nil {
		return mailbox.ErrNoPush
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.push:
			changed()
		}
	}
}

func (f *fake) Close() error { return nil }

// harness runs an engine and collects what it says.
type harness struct {
	t   *testing.T
	in  *io.PipeWriter
	out chan map[string]any
}

func start(t *testing.T, s Settings, accounts []Account, setup ...func(*Engine)) *harness {
	dir := t.TempDir()
	c, err := cache.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	e := New(c, filepath.Join(dir, "view"), outW, log.New(io.Discard, "", 0), s, accounts)
	for _, f := range setup {
		f(e)
	}
	h := &harness{t: t, in: inW, out: make(chan map[string]any, 1000)}
	go func() {
		dec := json.NewDecoder(outR)
		for {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return
			}
			h.out <- m
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx, inR); close(done) }()
	t.Cleanup(func() {
		h.send(map[string]any{"t": "quit"})
		cancel()
		<-done
		outW.Close()
	})
	return h
}

func (h *harness) send(cmd map[string]any) {
	b, _ := json.Marshal(cmd)
	h.in.Write(append(b, '\n'))
}

// wait returns the first message matching ok, failing after a while.
func (h *harness) wait(what string, ok func(map[string]any) bool) map[string]any {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-h.out:
			if ok(m) {
				return m
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func isT(t string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["t"] == t }
}

func subjectsOf(m map[string]any) []string {
	var out []string
	for _, x := range m["messages"].([]any) {
		out = append(out, x.(map[string]any)["subject"].(string))
	}
	return out
}

var fast = Settings{Window: 30 * 24 * time.Hour, Poll: 50 * time.Millisecond, PushPoll: time.Hour,
	Retry: 20 * time.Millisecond, Debounce: 0, OlderPage: 2}

func TestEngineEndToEnd(t *testing.T) {
	now := time.Now()
	a, b := newFake(true), newFake(false)
	a.add("a1", "a-new", now.Add(-time.Hour))
	a.add("a2", "a-old", now.AddDate(0, 0, -10))
	a.add("a3", "a-ancient1", now.AddDate(0, -3, 0))
	a.add("a4", "a-ancient2", now.AddDate(0, -4, 0))
	a.add("a5", "a-ancient3", now.AddDate(0, -5, 0))
	b.add("b1", "b-mid", now.AddDate(0, 0, -2))

	h := start(t, fast, []Account{{ID: "a@x", Kind: "gmail", Provider: a}, {ID: "b@x", Kind: "outlook", Provider: b}})
	ok := map[any]bool{}
	h.wait("both accounts ok", func(m map[string]any) bool {
		if m["t"] == "account" && m["account"].(map[string]any)["status"] == "ok" {
			ok[m["account"].(map[string]any)["id"]] = true
		}
		return len(ok) == 2
	})

	h.send(map[string]any{"t": "hello", "req": 1})
	hello := h.wait("hello", isT("hello"))
	if hello["req"] != float64(1) || len(hello["accounts"].([]any)) != 2 {
		t.Fatalf("hello %v", hello)
	}

	// Unified inbox, newest first, across accounts.
	h.send(map[string]any{"t": "list", "view": "unified", "limit": 3})
	l := h.wait("list", isT("list"))
	if got := fmt.Sprint(subjectsOf(l)); got != "[a-new b-mid a-old]" || l["more"] != true {
		t.Fatalf("page 1 %s more=%v", got, l["more"])
	}
	// Next page reaches past the window: older mail comes from the server.
	last := l["messages"].([]any)[2].(map[string]any)
	h.send(map[string]any{"t": "list", "view": "unified", "limit": 3, "beforeDate": last["date"], "beforeId": last["id"]})
	l = h.wait("list 2", isT("list"))
	if got := fmt.Sprint(subjectsOf(l)); got != "[a-ancient1 a-ancient2]" {
		t.Fatalf("page 2 %s", got)
	}
	last = l["messages"].([]any)[1].(map[string]any)
	h.send(map[string]any{"t": "list", "view": "unified", "limit": 3, "beforeDate": last["date"], "beforeId": last["id"]})
	l = h.wait("list 3", isT("list"))
	if got := fmt.Sprint(subjectsOf(l)); got != "[a-ancient3]" || l["more"] != false {
		t.Fatalf("page 3 %s more=%v", got, l["more"])
	}

	// New mail: pushed for a, polled for b.
	a.add("a6", "pushed", now)
	n := h.wait("newmail a", isT("newmail"))
	if n["subject"] != "pushed" || n["account"] != "a@x" || n["from"] != "Ann" {
		t.Fatalf("newmail %v", n)
	}
	b.add("b2", "polled", now)
	n = h.wait("newmail b", isT("newmail"))
	if n["subject"] != "polled" {
		t.Fatalf("newmail %v", n)
	}

	// Open renders a page without marking anything read.
	id := n["id"]
	h.send(map[string]any{"t": "open", "id": id})
	body := h.wait("body", isT("body"))
	page := body["page"].(map[string]any)
	if !strings.HasPrefix(page["url"].(string), "https://trashbox.view/") || body["subject"] != "polled" {
		t.Fatalf("body %v", body)
	}
	doc, err := os.ReadFile(page["path"].(string))
	if err != nil || !strings.Contains(string(doc), "<p>Hello <b>there</b></p>") {
		t.Fatalf("page %s %v", doc, err)
	}
	if len(b.seenOK) != 0 {
		t.Fatal("open must not mark read")
	}
	// A second open comes from the cache.
	h.send(map[string]any{"t": "open", "id": id})
	h.wait("body again", isT("body"))
	if b.raws != 1 {
		t.Fatalf("raw fetched %d times", b.raws)
	}

	// Marking read goes to the server and the cache.
	h.send(map[string]any{"t": "seen", "ids": []any{id}, "seen": true})
	h.wait("changed", func(m map[string]any) bool { return m["t"] == "changed" && m["account"] == "b@x" })
	if len(b.seenOK) != 1 || b.seenOK[0] != "b2" {
		t.Fatalf("server seen %v", b.seenOK)
	}
	h.send(map[string]any{"t": "list", "view": "folder", "account": "b@x", "folder": "IN", "limit": 1})
	l = h.wait("folder list", isT("list"))
	if m := l["messages"].([]any)[0].(map[string]any); m["subject"] != "polled" || m["seen"] != true {
		t.Fatalf("after seen %v", m)
	}

	// Removal on the server reaches the cache.
	b.mu.Lock()
	delete(b.msgs, "b1")
	b.mu.Unlock()
	h.wait("changed after delete", func(m map[string]any) bool { return m["t"] == "changed" && m["account"] == "b@x" })
	h.send(map[string]any{"t": "list", "view": "folder", "account": "b@x", "folder": "IN"})
	l = h.wait("folder list 2", isT("list"))
	if got := fmt.Sprint(subjectsOf(l)); got != "[polled]" {
		t.Fatalf("after delete %s", got)
	}
}

func TestBadCommands(t *testing.T) {
	h := start(t, fast, nil)
	h.in.Write([]byte("not json\n"))
	h.wait("parse error", isT("error"))
	h.send(map[string]any{"t": "nope", "req": "x"})
	e := h.wait("unknown", isT("error"))
	if e["req"] != "x" {
		t.Fatalf("%v", e)
	}
	h.send(map[string]any{"t": "open", "id": 999})
	h.wait("missing message", isT("error"))
}

func TestCodeInNewMail(t *testing.T) {
	a := newFake(true)
	a.add("old", "Hello", time.Now().Add(-time.Hour))
	got := make(chan Code, 4)
	h := start(t, fast, []Account{{ID: "a@x", Kind: "gmail", Provider: a}}, func(e *Engine) {
		e.OnCode = func(c Code) { got <- c }
	})
	h.wait("ok", func(m map[string]any) bool {
		return m["t"] == "account" && m["account"].(map[string]any)["status"] == "ok"
	})
	a.add("promo", "Big sale this weekend", time.Now())
	a.add("c1", "Your sign-in code", time.Now())
	ev := h.wait("code event", isT("code"))
	if ev["code"] != "482913" || ev["from"] != "Ann" || ev["account"] != "a@x" {
		t.Fatalf("code event %v", ev)
	}
	select {
	case c := <-got:
		if c.Code != "482913" || c.From != "Ann" {
			t.Fatalf("OnCode %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnCode not called")
	}
	select {
	case c := <-got:
		t.Fatalf("only one code expected, got another: %+v", c)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestReplyAndSend(t *testing.T) {
	a := newFake(false)
	a.add("a1", "Lunch", time.Now().Add(-time.Hour))
	var sent []compose.Outgoing
	var mu sync.Mutex
	send := func(_ context.Context, o compose.Outgoing) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, o)
		return nil
	}
	h := start(t, fast, []Account{{ID: "a@x", Kind: "gmail", Provider: a, Send: send}, {ID: "b@x", Kind: "gmail", Provider: newFake(false)}})
	h.wait("synced", func(m map[string]any) bool { return m["t"] == "changed" })
	h.send(map[string]any{"t": "list", "view": "unified", "req": 1})
	id := h.wait("list", isT("list"))["messages"].([]any)[0].(map[string]any)["id"]

	h.send(map[string]any{"t": "draft", "id": id, "all": true, "req": 2})
	d := h.wait("draft", isT("draft"))["draft"].(map[string]any)
	if d["from"] != "a@x" || d["to"] != "Ann <ann@x.com>" || d["subject"] != "Re: Lunch" || !strings.Contains(d["body"].(string), "> Hello there") {
		t.Fatalf("draft %v", d)
	}

	d["body"] = "Sounds good.\n" + d["body"].(string)
	h.send(map[string]any{"t": "send", "draft": d, "req": 3})
	if r := h.wait("sent", func(m map[string]any) bool { return m["req"] == float64(3) }); r["t"] != "sent" {
		t.Fatalf("send reply %v", r)
	}
	mu.Lock()
	if len(sent) != 1 || sent[0].Rcpts[0] != "ann@x.com" || !strings.Contains(string(sent[0].Raw), "Sounds good.") {
		t.Errorf("sent %+v", sent)
	}
	mu.Unlock()

	// Mistakes come back as errors, against the request.
	d["to"] = "ann@"
	h.send(map[string]any{"t": "send", "draft": d, "req": 4})
	if r := h.wait("bad address", func(m map[string]any) bool { return m["req"] == float64(4) }); r["t"] != "error" {
		t.Errorf("bad address: %v", r)
	}
	d["to"], d["from"] = "ann@x.com", "b@x"
	h.send(map[string]any{"t": "send", "draft": d, "req": 5})
	if r := h.wait("can't send", func(m map[string]any) bool { return m["req"] == float64(5) }); r["t"] != "error" {
		t.Errorf("account without sending: %v", r)
	}
}

func TestSaveAttachmentAndNewMail(t *testing.T) {
	a := newFake(false)
	now := time.Now()
	a.add("a1", "Fresh", now.Add(-time.Hour))
	a.add("a2", "Old", now.Add(-72*time.Hour))
	got := make(chan []NewMail, 10)
	h := start(t, fast, []Account{{ID: "a@x", Kind: "gmail", Provider: a}}, func(e *Engine) {
		e.Inbox = func(_ context.Context, m []NewMail) error { got <- m; return nil }
	})
	var mail []NewMail
	deadline := time.After(10 * time.Second)
	for len(mail) == 0 {
		select {
		case mail = <-got:
		case <-deadline:
			t.Fatal("no new mail reported")
		}
	}
	if len(mail) != 1 || mail[0].Subject != "Fresh" || !strings.Contains(mail[0].URL, "mail.google.com/mail/u/a@x/?tb=") {
		t.Fatalf("new mail %+v", mail)
	}

	// Reading it takes it off the list.
	var id int64
	fmt.Sscanf(strings.TrimPrefix(mail[0].Key, "a@x:"), "%d", &id)
	h.send(map[string]any{"t": "seen", "ids": []int64{id}, "seen": true})
	for {
		select {
		case m := <-got:
			if len(m) == 0 {
				goto saved
			}
		case <-time.After(10 * time.Second):
			t.Fatal("reading it didn't clear it")
		}
	}
saved:
	// The fake's messages have no attachments.
	path := filepath.Join(t.TempDir(), "x.pdf")
	h.send(map[string]any{"t": "save", "id": id, "index": 0, "path": path, "req": 9})
	if r := h.wait("save", func(m map[string]any) bool { return m["req"] == float64(9) }); r["t"] != "error" {
		t.Errorf("saving a missing attachment: %v", r)
	}
	h.send(map[string]any{"t": "save", "id": id, "index": 0, "path": "relative.pdf", "req": 10})
	if r := h.wait("save", func(m map[string]any) bool { return m["req"] == float64(10) }); r["t"] != "error" {
		t.Errorf("saving to a relative path: %v", r)
	}
}
