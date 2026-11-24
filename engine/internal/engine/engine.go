// Package engine is trashbox-engine's main loop: one worker per account
// keeping the cache in step with the server, and the protocol the window
// speaks over stdin/stdout.
//
// The window's commands, one JSON object a line on stdin, each with "t" and
// an optional "req" that comes back on the reply:
//
//	hello                                  -> {"t":"hello","accounts":[...]}
//	folders account                        -> {"t":"folders","account","folders":[...]}
//	list view account folder beforeDate beforeId limit
//	                                       -> {"t":"list","view","account","folder","messages":[...],"more"}
//	   view is "unified" (every inbox) or "folder"; before* is the last
//	   message already shown, for the next page
//	open id [remote]                       -> {"t":"body","id","page":{url,hasRemote,remote},"from",...}
//	   doesn't mark the message read: send "seen" for that
//	seen ids seen                          -> marks read/unread on the server and in the cache
//	draft id all                           -> {"t":"draft","draft":{from,to,cc,bcc,subject,body,inReplyTo,references}}
//	   a reply to message id (all: to everyone on it), from its account
//	send draft                             -> {"t":"sent","account"} once the server has it
//	sync                                   -> checks every account now
//	quit
//
// Unasked: {"t":"account"} (status changes), {"t":"folders"} (counts
// changed), {"t":"changed","account","folder"} (the list should be
// fetched again), {"t":"newmail","account","id","from","subject"},
// {"t":"code","account","id","from","code"} (a sign-in code in new mail),
// {"t":"error","req","error"}.
package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/cache"
	"github.com/seventy-two/trashbox/engine/internal/codes"
	"github.com/seventy-two/trashbox/engine/internal/compose"
	"github.com/seventy-two/trashbox/engine/internal/htmltext"
	"github.com/seventy-two/trashbox/engine/internal/mailbox"
	"github.com/seventy-two/trashbox/engine/internal/mailparse"
	"github.com/seventy-two/trashbox/engine/internal/view"
)

// Settings tune the engine; tests shorten the times.
type Settings struct {
	Window    time.Duration // how far back the first sync of a folder goes
	Poll      time.Duration // how often accounts without push are checked
	PushPoll  time.Duration // a safety check for accounts with push
	Retry     time.Duration // wait after a failure (doubles up to 10 min)
	Debounce  time.Duration // gather bursts of push notices into one sync
	OlderPage int           // messages fetched per trip into older mail
}

// DefaultSettings are the real ones.
var DefaultSettings = Settings{
	Window: 60 * 24 * time.Hour, Poll: time.Minute, PushPoll: 5 * time.Minute,
	Retry: 30 * time.Second, Debounce: time.Second, OlderPage: 100,
}

// Code is a sign-in code found in new mail.
type Code struct {
	Account, From, Code string
	ID                  int64
}

// Account is one account to run.
type Account struct {
	ID       string // the address
	Kind     string
	Provider mailbox.Provider
	// Send sends a message from the account; nil if it can't.
	Send func(ctx context.Context, m compose.Outgoing) error
}

// Engine runs the accounts and answers the window.
type Engine struct {
	cache    *cache.Cache
	viewDir  string
	log      *log.Logger
	settings Settings

	outMu sync.Mutex
	out   *json.Encoder

	accounts []*account
	byID     map[string]*account

	viewMu sync.Mutex
	views  []string // page folders written, oldest first

	// OnCode, if set, is called (on its own goroutine) for each sign-in
	// code found in new mail. Set it before Run.
	OnCode func(Code)
}

type account struct {
	Account
	e    *Engine
	kick chan struct{}

	mu     sync.Mutex // one sync or older-mail fetch at a time
	inbox  string
	active string // a non-inbox folder the window is showing

	stMu   sync.Mutex
	status string // "connecting", "syncing", "ok", "error"
	err    string
}

// New makes an engine. Output goes to out as JSON lines.
func New(c *cache.Cache, viewDir string, out io.Writer, logger *log.Logger, s Settings, accounts []Account) *Engine {
	e := &Engine{cache: c, viewDir: viewDir, log: logger, settings: s, out: json.NewEncoder(out), byID: map[string]*account{}}
	for _, a := range accounts {
		acc := &account{Account: a, e: e, kick: make(chan struct{}, 1), status: "connecting"}
		e.accounts = append(e.accounts, acc)
		e.byID[a.ID] = acc
	}
	return e
}

// Emit writes one message to the window.
func (e *Engine) Emit(v map[string]any) {
	e.outMu.Lock()
	defer e.outMu.Unlock()
	if err := e.out.Encode(v); err != nil {
		e.log.Printf("emit: %v", err)
	}
}

// Run starts the account workers and handles commands from in until it
// ends or a quit arrives.
func (e *Engine) Run(ctx context.Context, in io.Reader) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, a := range e.accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.run(ctx)
		}()
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(sc.Bytes(), &cmd); err != nil {
			e.Emit(map[string]any{"t": "error", "error": "bad command: " + err.Error()})
			continue
		}
		if str(cmd, "t") == "quit" {
			break
		}
		go e.handle(ctx, cmd)
	}
	cancel()
	wg.Wait()
	for _, a := range e.accounts {
		a.Provider.Close()
	}
}

func str(cmd map[string]any, k string) string {
	s, _ := cmd[k].(string)
	return s
}

func num(cmd map[string]any, k string) int64 {
	f, _ := cmd[k].(float64)
	return int64(f)
}

func (e *Engine) handle(ctx context.Context, cmd map[string]any) {
	req := cmd["req"]
	reply := func(v map[string]any) {
		if req != nil {
			v["req"] = req
		}
		e.Emit(v)
	}
	defer func() {
		if r := recover(); r != nil {
			e.log.Printf("command %v panicked: %v", cmd["t"], r)
			reply(map[string]any{"t": "error", "error": fmt.Sprint(r)})
		}
	}()
	var err error
	switch str(cmd, "t") {
	case "hello":
		reply(map[string]any{"t": "hello", "accounts": e.accountList()})
	case "folders":
		err = e.cmdFolders(cmd, reply)
	case "list":
		err = e.cmdList(ctx, cmd, reply)
	case "open":
		err = e.cmdOpen(ctx, cmd, reply)
	case "seen":
		err = e.cmdSeen(ctx, cmd)
	case "draft":
		err = e.cmdDraft(ctx, cmd, reply)
	case "send":
		err = e.cmdSend(ctx, cmd, reply)
	case "sync":
		for _, a := range e.accounts {
			a.poke()
		}
	default:
		err = fmt.Errorf("unknown command %q", str(cmd, "t"))
	}
	if err != nil {
		reply(map[string]any{"t": "error", "error": err.Error()})
	}
}

func (e *Engine) accountList() []map[string]any {
	out := []map[string]any{}
	for _, a := range e.accounts {
		out = append(out, a.info())
	}
	return out
}

func (e *Engine) account(id string) (*account, error) {
	a := e.byID[id]
	if a == nil {
		return nil, fmt.Errorf("no account %q", id)
	}
	return a, nil
}

func (e *Engine) cmdFolders(cmd map[string]any, reply func(map[string]any)) error {
	a, err := e.account(str(cmd, "account"))
	if err != nil {
		return err
	}
	fs, err := e.folderList(a.ID)
	if err != nil {
		return err
	}
	reply(map[string]any{"t": "folders", "account": a.ID, "folders": fs})
	return nil
}

func (e *Engine) folderList(account string) ([]mailbox.Folder, error) {
	fs, err := e.cache.Folders(account)
	if err != nil {
		return nil, err
	}
	out := make([]mailbox.Folder, len(fs))
	for i, f := range fs {
		out[i] = f.Folder
	}
	return out, nil
}

func (e *Engine) cmdList(ctx context.Context, cmd map[string]any, reply func(map[string]any)) error {
	q := cache.Query{
		Unified: str(cmd, "view") != "folder", Account: str(cmd, "account"), Folder: str(cmd, "folder"),
		BeforeDate: num(cmd, "beforeDate"), BeforeID: num(cmd, "beforeId"), Limit: int(num(cmd, "limit")),
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	// Which folders feed this view, so we know where older mail comes from.
	type src struct {
		a      *account
		folder string
	}
	var srcs []src
	if q.Unified {
		for _, a := range e.accounts {
			if in := a.inboxID(); in != "" {
				srcs = append(srcs, src{a, in})
			}
		}
	} else {
		a, err := e.account(q.Account)
		if err != nil {
			return err
		}
		a.show(ctx, q.Folder)
		srcs = append(srcs, src{a, q.Folder})
	}

	ms, err := e.cache.List(q)
	if err != nil {
		return err
	}
	more := len(ms) == q.Limit
	if !more {
		// The cache ran out: fetch older mail from each source that has more,
		// then look again.
		fetched := false
		for _, s := range srcs {
			got, err := s.a.fetchOlder(ctx, s.folder)
			if err != nil {
				e.log.Printf("%s: older mail: %v", s.a.ID, err)
				continue
			}
			fetched = fetched || got
		}
		if fetched {
			if ms, err = e.cache.List(q); err != nil {
				return err
			}
		}
		more = len(ms) == q.Limit
		for _, s := range srcs {
			if f, err := e.cache.Folder(s.a.ID, s.folder); err == nil && !f.Complete {
				more = true
			}
		}
	}
	reply(map[string]any{"t": "list", "view": str(cmd, "view"), "account": q.Account, "folder": q.Folder,
		"beforeDate": q.BeforeDate, "beforeId": q.BeforeID, "messages": ms, "more": more})
	return nil
}

func (e *Engine) cmdOpen(ctx context.Context, cmd map[string]any, reply func(map[string]any)) error {
	id := num(cmd, "id")
	m, parsed, err := e.parsed(ctx, id)
	if err != nil {
		return err
	}
	remote, _ := cmd["remote"].(bool)
	page, err := view.Write(e.viewDir, id, parsed, remote)
	if err != nil {
		return err
	}
	e.keepView(fmt.Sprint(id))
	reply(map[string]any{
		"t": "body", "id": id, "page": page,
		"subject": parsed.Subject, "from": parsed.From, "to": parsed.To, "cc": parsed.Cc,
		"replyTo": parsed.ReplyTo, "date": m.Date, "attachments": nonNil(parsed.Attachments),
	})
	return nil
}

func nonNil(a []mailparse.Attachment) []mailparse.Attachment {
	if a == nil {
		return []mailparse.Attachment{}
	}
	return a
}

func snippetOf(m *mailparse.Message) string {
	text := m.Text
	if text == "" {
		text = htmltext.Convert(m.HTML)
	}
	return htmltext.Snippet(text, 200)
}

// keepView remembers the pages written and prunes all but the last few.
func (e *Engine) keepView(name string) {
	e.viewMu.Lock()
	defer e.viewMu.Unlock()
	e.views = slices.DeleteFunc(e.views, func(s string) bool { return s == name })
	e.views = append(e.views, name)
	if len(e.views) > 20 {
		e.views = e.views[len(e.views)-20:]
	}
	keep := map[string]bool{}
	for _, v := range e.views {
		keep[v] = true
	}
	view.Prune(e.viewDir, keep)
}

// parsed returns a message's parsed source, fetching it if it isn't cached.
func (e *Engine) parsed(ctx context.Context, id int64) (cache.Message, *mailparse.Message, error) {
	m, err := e.cache.Get(id)
	if err != nil {
		return m, nil, fmt.Errorf("message %d: %w", id, err)
	}
	raw, ok, err := e.cache.Body(id)
	if err != nil {
		return m, nil, err
	}
	if !ok {
		a, err := e.account(m.Account)
		if err != nil {
			return m, nil, err
		}
		if raw, err = a.Provider.Raw(ctx, m.Folder, m.RID); err != nil {
			return m, nil, err
		}
	}
	p, err := mailparse.Parse(raw)
	if err != nil {
		return m, nil, err
	}
	if !ok {
		if err := e.cache.SaveBody(id, raw, snippetOf(p)); err != nil {
			e.log.Printf("saving body %d: %v", id, err)
		}
	}
	return m, p, nil
}

func (e *Engine) cmdDraft(ctx context.Context, cmd map[string]any, reply func(map[string]any)) error {
	m, p, err := e.parsed(ctx, num(cmd, "id"))
	if err != nil {
		return err
	}
	all, _ := cmd["all"].(bool)
	reply(map[string]any{"t": "draft", "id": m.ID, "draft": compose.Reply(p, m.Account, all)})
	return nil
}

func (e *Engine) cmdSend(ctx context.Context, cmd map[string]any, reply func(map[string]any)) error {
	var d compose.Draft
	b, _ := json.Marshal(cmd["draft"])
	if err := json.Unmarshal(b, &d); err != nil {
		return fmt.Errorf("bad draft: %w", err)
	}
	a, err := e.account(d.From)
	if err != nil {
		return err
	}
	if a.Send == nil {
		return fmt.Errorf("%s can't send mail", a.ID)
	}
	out, err := compose.Build(d, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := a.Send(ctx, out); err != nil {
		e.log.Printf("%s: sending: %v", a.ID, err)
		return fmt.Errorf("not sent: %w", err)
	}
	e.log.Printf("%s: sent a message to %d recipient(s)", a.ID, len(out.Rcpts))
	reply(map[string]any{"t": "sent", "account": a.ID})
	a.poke() // Sent has changed
	return nil
}

func (e *Engine) cmdSeen(ctx context.Context, cmd map[string]any) error {
	seen, _ := cmd["seen"].(bool)
	raw, _ := cmd["ids"].([]any)
	type key struct{ account, folder string }
	groups := map[key][]cache.Message{}
	for _, v := range raw {
		f, _ := v.(float64)
		m, err := e.cache.Get(int64(f))
		if err != nil {
			continue
		}
		k := key{m.Account, m.Folder}
		groups[k] = append(groups[k], m)
	}
	for k, ms := range groups {
		a, err := e.account(k.account)
		if err != nil {
			return err
		}
		rids := make([]string, len(ms))
		ids := make([]int64, len(ms))
		for i, m := range ms {
			rids[i], ids[i] = m.RID, m.ID
		}
		if err := a.Provider.SetSeen(ctx, k.folder, rids, seen); err != nil {
			return err
		}
		if err := e.cache.SetSeen(ids, seen); err != nil {
			return err
		}
		e.Emit(map[string]any{"t": "changed", "account": k.account, "folder": k.folder})
		if fs, err := e.folderList(k.account); err == nil {
			e.Emit(map[string]any{"t": "folders", "account": k.account, "folders": fs})
		}
	}
	return nil
}

// ---- account worker ----

func (a *account) info() map[string]any {
	a.stMu.Lock()
	defer a.stMu.Unlock()
	return map[string]any{"id": a.ID, "kind": a.Kind, "status": a.status, "error": a.err}
}

func (a *account) setStatus(status string, err error) {
	a.stMu.Lock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed := a.status != status || a.err != msg
	a.status, a.err = status, msg
	a.stMu.Unlock()
	if changed {
		a.e.Emit(map[string]any{"t": "account", "account": a.info()})
	}
}

func (a *account) inboxID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inbox
}

func (a *account) poke() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// show is called when the window opens a folder: it's synced now if it
// never has been, and kept in step from then on.
func (a *account) show(ctx context.Context, folder string) {
	a.mu.Lock()
	if folder != a.inbox {
		a.active = folder
	}
	a.mu.Unlock()
	f, err := a.e.cache.Folder(a.ID, folder)
	if err == nil && f.State == "" {
		if _, err := a.syncFolder(ctx, folder); err != nil {
			a.e.log.Printf("%s: first sync of %s: %v", a.ID, folder, err)
		}
	}
}

func (a *account) run(ctx context.Context) {
	s := a.e.settings
	backoff := s.Retry
	wait := func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-a.kick:
			return true
		case <-t.C:
			return true
		}
	}

	// Folders first: nothing works without knowing the inbox.
	for {
		err := a.refreshFolders(ctx)
		if err == nil {
			break
		}
		a.e.log.Printf("%s: folders: %v", a.ID, err)
		a.setStatus("error", err)
		if !wait(backoff) {
			return
		}
		backoff = min(backoff*2, 10*time.Minute)
	}
	backoff = s.Retry

	// Push, where the account has it.
	poll := s.Poll
	pushCtx, stopPush := context.WithCancel(ctx)
	defer stopPush()
	pushing := make(chan bool, 1)
	go a.watch(pushCtx, pushing)
	if <-pushing {
		poll = s.PushPoll
	}

	first := true
	for {
		if first {
			a.setStatus("syncing", nil) // later syncs are quiet: they're quick
		}
		err := a.syncAll(ctx, first)
		if ctx.Err() != nil {
			return
		}
		next := poll
		if err != nil {
			a.e.log.Printf("%s: sync: %v", a.ID, err)
			a.setStatus("error", err)
			next = min(backoff, poll)
			backoff = min(backoff*2, 10*time.Minute)
		} else {
			a.setStatus("ok", nil)
			backoff = s.Retry
			first = false
		}
		if !wait(next) {
			return
		}
		// Let a burst of notices settle into one sync.
		if s.Debounce > 0 {
			t := time.NewTimer(s.Debounce)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			select {
			case <-a.kick:
			default:
			}
		}
	}
}

// watch keeps the provider's push running, reporting on pushing whether
// there is any.
func (a *account) watch(ctx context.Context, pushing chan<- bool) {
	reported := false
	report := func(v bool) {
		if !reported {
			reported = true
			pushing <- v
		}
	}
	for ctx.Err() == nil {
		inbox := a.inboxID()
		started := time.Now()
		// Treat an IDLE that lasted a while as push working.
		timer := time.AfterFunc(2*time.Second, func() { report(true) })
		err := a.Provider.Watch(ctx, inbox, a.poke)
		timer.Stop()
		if errors.Is(err, mailbox.ErrNoPush) {
			report(false)
			return
		}
		if ctx.Err() != nil {
			report(false)
			return
		}
		if time.Since(started) < 2*time.Second {
			report(false) // failing fast: poll until it works
		}
		a.e.log.Printf("%s: push: %v", a.ID, err)
		t := time.NewTimer(a.e.settings.Retry)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		a.poke() // catch up on whatever happened while it was down
	}
}

func (a *account) refreshFolders(ctx context.Context) error {
	fs, err := a.Provider.Folders(ctx)
	if err != nil {
		return err
	}
	if err := a.e.cache.SaveFolders(a.ID, fs); err != nil {
		return err
	}
	a.mu.Lock()
	for _, f := range fs {
		if f.Role == mailbox.RoleInbox {
			a.inbox = f.ID
		}
	}
	inbox := a.inbox
	a.mu.Unlock()
	if inbox == "" {
		return errors.New("no inbox found")
	}
	a.e.Emit(map[string]any{"t": "folders", "account": a.ID, "folders": fs})
	return nil
}

func (a *account) syncAll(ctx context.Context, first bool) error {
	a.mu.Lock()
	inbox, active := a.inbox, a.active
	a.mu.Unlock()

	added, err := a.syncFolder(ctx, inbox)
	if err != nil {
		return err
	}
	if !first {
		a.announce(added)
	}
	if active != "" && active != inbox {
		if _, err := a.syncFolder(ctx, active); err != nil {
			return err
		}
	}
	return a.refreshFolders(ctx)
}

// syncFolder brings one folder's window up to date, returning new message
// IDs (empty on the folder's first sync, which isn't news).
func (a *account) syncFolder(ctx context.Context, folder string) ([]int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := a.e.cache.Folder(a.ID, folder)
	if err != nil {
		return nil, fmt.Errorf("folder %s: %w", folder, err)
	}
	since := f.Since
	if since.IsZero() {
		since = time.Now().Add(-a.e.settings.Window).Truncate(24 * time.Hour)
	}
	known, err := a.e.cache.Known(a.ID, folder, since)
	if err != nil {
		return nil, err
	}
	d, err := a.Provider.Sync(ctx, folder, f.State, since, known)
	if err != nil {
		return nil, err
	}
	added, err := a.e.cache.Apply(a.ID, folder, since, d)
	if err != nil {
		return nil, err
	}
	if err := a.e.cache.SetSync(a.ID, folder, d.State, since); err != nil {
		return nil, err
	}
	if len(d.New)+len(d.Flags)+len(d.Removed) > 0 {
		a.e.Emit(map[string]any{"t": "changed", "account": a.ID, "folder": folder})
	}
	if f.State == "" {
		return nil, nil
	}
	return added, nil
}

// announce tells the window about new unread mail.
func (a *account) announce(ids []int64) {
	for _, id := range ids {
		m, err := a.e.cache.Get(id)
		if err != nil || m.Seen {
			continue
		}
		if time.Since(time.Unix(m.Date, 0)) > 24*time.Hour {
			continue // old mail turning up (e.g. moved back into the inbox) isn't news
		}
		from := m.FromName
		if from == "" {
			from = m.FromAddr
		}
		a.e.Emit(map[string]any{"t": "newmail", "account": a.ID, "id": id, "from": from, "subject": m.Subject})
		// Codes go stale in minutes: only fresh mail is worth reading for one.
		if time.Since(time.Unix(m.Date, 0)) < 15*time.Minute && codes.Looks(m.Subject, m.Snippet) {
			go a.findCode(m, from)
		}
	}
}

// findCode reads a new message in full for a sign-in code.
func (a *account) findCode(m cache.Message, from string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, ok, err := a.e.cache.Body(m.ID)
	if err != nil {
		return
	}
	if !ok {
		if raw, err = a.Provider.Raw(ctx, m.Folder, m.RID); err != nil {
			a.e.log.Printf("%s: reading %d for a code: %v", a.ID, m.ID, err)
			return
		}
	}
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		return
	}
	if !ok {
		a.e.cache.SaveBody(m.ID, raw, snippetOf(parsed))
	}
	text := parsed.Text
	if strings.TrimSpace(text) == "" {
		text = htmltext.Convert(parsed.HTML)
	}
	code := codes.Find(m.Subject, text)
	if code == "" {
		return
	}
	a.e.log.Printf("%s: sign-in code in message %d from %s", a.ID, m.ID, from) // never the code itself
	a.e.Emit(map[string]any{"t": "code", "account": a.ID, "id": m.ID, "from": from, "code": code})
	if a.e.OnCode != nil {
		go a.e.OnCode(Code{Account: a.ID, From: from, Code: code, ID: m.ID})
	}
}

// fetchOlder pulls the next page of mail from before what's cached. It
// reports whether anything new arrived.
func (a *account) fetchOlder(ctx context.Context, folder string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := a.e.cache.Folder(a.ID, folder)
	if err != nil {
		return false, err
	}
	if f.Complete || f.State == "" {
		return false, nil
	}
	before, ok, err := a.e.cache.Oldest(a.ID, folder)
	if err != nil {
		return false, err
	}
	if !ok || before.After(f.Since) {
		before = f.Since
	}
	hs, more, err := a.Provider.Older(ctx, folder, before, a.e.settings.OlderPage)
	if err != nil {
		return false, err
	}
	if err := a.e.cache.AddOlder(a.ID, folder, hs); err != nil {
		return false, err
	}
	if !more {
		if err := a.e.cache.SetComplete(a.ID, folder, true); err != nil {
			return false, err
		}
	}
	return len(hs) > 0, nil
}
