// Package cache is trashbox's local copy of folders, message headers and
// the messages opened so far, in SQLite. The servers stay the truth; the
// cache can be deleted at any time and rebuilt.
package cache

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

// Cache is the database.
type Cache struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS folders(
	account TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, parent TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '', unread INT NOT NULL DEFAULT 0, total INT NOT NULL DEFAULT 0,
	pos INT NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT '', since INT NOT NULL DEFAULT 0, complete INT NOT NULL DEFAULT 0,
	PRIMARY KEY(account, id));
CREATE TABLE IF NOT EXISTS messages(
	id INTEGER PRIMARY KEY, account TEXT NOT NULL, folder TEXT NOT NULL, rid TEXT NOT NULL,
	date INT NOT NULL, from_name TEXT NOT NULL DEFAULT '', from_addr TEXT NOT NULL DEFAULT '',
	to_text TEXT NOT NULL DEFAULT '', subject TEXT NOT NULL DEFAULT '', snippet TEXT NOT NULL DEFAULT '',
	seen INT NOT NULL DEFAULT 0, flagged INT NOT NULL DEFAULT 0, attach INT NOT NULL DEFAULT 0,
	UNIQUE(account, folder, rid));
CREATE INDEX IF NOT EXISTS messages_folder ON messages(account, folder, date DESC, id DESC);
CREATE INDEX IF NOT EXISTS messages_date ON messages(date DESC, id DESC);
CREATE TABLE IF NOT EXISTS bodies(
	id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
	raw BLOB NOT NULL, fetched INT NOT NULL);
`

// Open opens (creating if needed) the database at path.
func Open(path string) (*Cache, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; the engine's load is small
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache schema: %w", err)
	}
	// found (added for search): 1 for a message known only from a search.
	// Such messages open and show in results, but stay out of folder lists
	// and paging, or a folder's older mail would be fetched from the wrong
	// place.
	var has int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='found'`).Scan(&has)
	if has == 0 {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN found INT NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, fmt.Errorf("cache schema: %w", err)
		}
	}
	return &Cache{db}, nil
}

// Close closes the database.
func (c *Cache) Close() error { return c.db.Close() }

// Folder is a folder with its sync bookkeeping.
type Folder struct {
	mailbox.Folder
	State    string    `json:"-"`
	Since    time.Time `json:"-"` // start of the synced window; zero before the first sync
	Complete bool      `json:"-"` // everything older than the window is cached too
}

// SaveFolders replaces an account's folder list, keeping each folder's sync
// state. Folders that disappeared lose their messages.
func (c *Cache) SaveFolders(account string, fs []mailbox.Folder) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := make([]any, 0, len(fs)+1)
	ids = append(ids, account)
	for i, f := range fs {
		_, err := tx.Exec(`INSERT INTO folders(account,id,name,parent,role,unread,total,pos) VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(account,id) DO UPDATE SET name=excluded.name, parent=excluded.parent, role=excluded.role,
			unread=excluded.unread, total=excluded.total, pos=excluded.pos`,
			account, f.ID, f.Name, f.Parent, f.Role, f.Unread, f.Total, i)
		if err != nil {
			return err
		}
		ids = append(ids, f.ID)
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(fs)), ",")
	if in == "" {
		in = "NULL"
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE account=? AND folder NOT IN (`+in+`)`, ids...); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM folders WHERE account=? AND id NOT IN (`+in+`)`, ids...); err != nil {
		return err
	}
	return tx.Commit()
}

const folderCols = `id,name,parent,role,unread,total,state,since,complete`

func scanFolder(s interface{ Scan(...any) error }) (Folder, error) {
	var f Folder
	var since int64
	err := s.Scan(&f.ID, &f.Name, &f.Parent, &f.Role, &f.Unread, &f.Total, &f.State, &since, &f.Complete)
	if since > 0 {
		f.Since = time.Unix(since, 0)
	}
	return f, err
}

// Folders returns an account's folders in the server's order.
func (c *Cache) Folders(account string) ([]Folder, error) {
	rows, err := c.db.Query(`SELECT `+folderCols+` FROM folders WHERE account=? ORDER BY pos`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Folder returns one folder.
func (c *Cache) Folder(account, id string) (Folder, error) {
	return scanFolder(c.db.QueryRow(`SELECT `+folderCols+` FROM folders WHERE account=? AND id=?`, account, id))
}

// SetSync records a folder's sync state and window start.
func (c *Cache) SetSync(account, id, state string, since time.Time) error {
	_, err := c.db.Exec(`UPDATE folders SET state=?, since=? WHERE account=? AND id=?`, state, since.Unix(), account, id)
	return err
}

// SetComplete records that a folder's older mail is all cached.
func (c *Cache) SetComplete(account, id string, complete bool) error {
	_, err := c.db.Exec(`UPDATE folders SET complete=? WHERE account=? AND id=?`, complete, account, id)
	return err
}

// Known returns the folder's cached messages in the window: RID → date.
func (c *Cache) Known(account, folder string, since time.Time) (map[string]time.Time, error) {
	rows, err := c.db.Query(`SELECT rid, date FROM messages WHERE account=? AND folder=? AND date>=? AND found=0`, account, folder, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var rid string
		var d int64
		if err := rows.Scan(&rid, &d); err != nil {
			return nil, err
		}
		out[rid] = time.Unix(d, 0)
	}
	return out, rows.Err()
}

const upsert = `INSERT INTO messages(account,folder,rid,date,from_name,from_addr,to_text,subject,snippet,seen,flagged,attach)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(account,folder,rid) DO UPDATE SET date=excluded.date, from_name=excluded.from_name,
	from_addr=excluded.from_addr, to_text=excluded.to_text, subject=excluded.subject,
	snippet=CASE WHEN excluded.snippet<>'' THEN excluded.snippet ELSE messages.snippet END,
	seen=excluded.seen, flagged=excluded.flagged, attach=excluded.attach, found=0`

// upsertFound adds a search result, leaving a message already cached as it
// is (bar its flags).
const upsertFound = `INSERT INTO messages(account,folder,rid,date,from_name,from_addr,to_text,subject,snippet,seen,flagged,attach,found)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1)
	ON CONFLICT(account,folder,rid) DO UPDATE SET seen=excluded.seen, flagged=excluded.flagged,
	snippet=CASE WHEN messages.snippet='' THEN excluded.snippet ELSE messages.snippet END`

func put(tx *sql.Tx, account, folder string, h mailbox.Header) (id int64, inserted bool, err error) {
	var existing int64
	err = tx.QueryRow(`SELECT id FROM messages WHERE account=? AND folder=? AND rid=?`, account, folder, h.RID).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	res, err := tx.Exec(upsert, account, folder, h.RID, h.Date.Unix(), h.FromName, h.FromAddr, h.To, h.Subject, h.Snippet, h.Seen, h.Flagged, h.Attach)
	if err != nil {
		return 0, false, err
	}
	if existing != 0 {
		return existing, false, nil
	}
	id, err = res.LastInsertId()
	return id, true, err
}

// Apply stores a sync's changes and returns the IDs of messages new to the
// cache.
func (c *Cache) Apply(account, folder string, since time.Time, d mailbox.Delta) ([]int64, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var added []int64
	keep := map[string]bool{}
	for _, h := range d.New {
		id, inserted, err := put(tx, account, folder, h)
		if err != nil {
			return nil, err
		}
		if inserted {
			added = append(added, id)
		}
		keep[h.RID] = true
	}
	for _, f := range d.Flags {
		if _, err := tx.Exec(`UPDATE messages SET seen=?, flagged=? WHERE account=? AND folder=? AND rid=?`,
			f.Seen, f.Flagged, account, folder, f.RID); err != nil {
			return nil, err
		}
		keep[f.RID] = true
	}
	for _, rid := range d.Removed {
		if _, err := tx.Exec(`DELETE FROM messages WHERE account=? AND folder=? AND rid=?`, account, folder, rid); err != nil {
			return nil, err
		}
	}
	if d.Reset {
		rows, err := tx.Query(`SELECT rid FROM messages WHERE account=? AND folder=? AND date>=?`, account, folder, since.Unix())
		if err != nil {
			return nil, err
		}
		var gone []string
		for rows.Next() {
			var rid string
			rows.Scan(&rid)
			if !keep[rid] {
				gone = append(gone, rid)
			}
		}
		rows.Close()
		for _, rid := range gone {
			if _, err := tx.Exec(`DELETE FROM messages WHERE account=? AND folder=? AND rid=?`, account, folder, rid); err != nil {
				return nil, err
			}
		}
	}
	return added, tx.Commit()
}

// AddOlder stores headers fetched from before the window.
func (c *Cache) AddOlder(account, folder string, hs []mailbox.Header) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hs {
		if _, _, err := put(tx, account, folder, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddFound stores messages found by a server search.
func (c *Cache) AddFound(account, folder string, hs []mailbox.Header) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hs {
		if _, err := tx.Exec(upsertFound, account, folder, h.RID, h.Date.Unix(), h.FromName, h.FromAddr, h.To, h.Subject,
			h.Snippet, h.Seen, h.Flagged, h.Attach); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search finds cached messages whose subject, sender, recipients or
// snippet contain every word of q (case-insensitive), newest first. Junk
// and trash are left out.
func (c *Cache) Search(q string, limit int) ([]Message, error) {
	words := strings.Fields(q)
	if len(words) == 0 {
		return []Message{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where := []string{`COALESCE(f.role,'') NOT IN ('junk','trash')`}
	var args []any
	for _, w := range words {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(w)) + "%"
		where = append(where, `(lower(m.subject) LIKE ? ESCAPE '\' OR lower(m.from_name) LIKE ? ESCAPE '\' OR
			lower(m.from_addr) LIKE ? ESCAPE '\' OR lower(m.to_text) LIKE ? ESCAPE '\' OR lower(m.snippet) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like, like)
	}
	args = append(args, limit)
	rows, err := c.db.Query(`SELECT `+msgCols+` FROM messages m LEFT JOIN folders f ON f.account=m.account AND f.id=m.folder
		WHERE `+strings.Join(where, " AND ")+` ORDER BY m.date DESC, m.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Oldest returns the date of the folder's oldest cached message.
func (c *Cache) Oldest(account, folder string) (time.Time, bool, error) {
	var d sql.NullInt64
	err := c.db.QueryRow(`SELECT MIN(date) FROM messages WHERE account=? AND folder=? AND found=0`, account, folder).Scan(&d)
	if err != nil || !d.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(d.Int64, 0), true, nil
}

// Message is a cached message header, as the window sees it.
type Message struct {
	ID       int64  `json:"id"`
	Account  string `json:"account"`
	Folder   string `json:"folder"`
	RID      string `json:"-"`
	Date     int64  `json:"date"` // unix seconds
	FromName string `json:"fromName"`
	FromAddr string `json:"fromAddr"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Snippet  string `json:"snippet"`
	Seen     bool   `json:"seen"`
	Flagged  bool   `json:"flagged"`
	Attach   bool   `json:"attach"`
}

const msgCols = `m.id,m.account,m.folder,m.rid,m.date,m.from_name,m.from_addr,m.to_text,m.subject,m.snippet,m.seen,m.flagged,m.attach`

func scanMessage(s interface{ Scan(...any) error }) (Message, error) {
	var m Message
	err := s.Scan(&m.ID, &m.Account, &m.Folder, &m.RID, &m.Date, &m.FromName, &m.FromAddr, &m.To, &m.Subject, &m.Snippet, &m.Seen, &m.Flagged, &m.Attach)
	return m, err
}

// Query picks a page of the message list. With Unified set, it's every
// account's inbox together; otherwise one folder. Before* is the cursor:
// the last message of the previous page.
type Query struct {
	Unified    bool
	Account    string
	Folder     string
	BeforeDate int64
	BeforeID   int64
	Limit      int
}

// List returns a page of messages, newest first.
func (c *Cache) List(q Query) ([]Message, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	where := []string{`m.found=0`}
	var args []any
	from := `messages m`
	if q.Unified {
		from += ` JOIN folders f ON f.account=m.account AND f.id=m.folder AND f.role='inbox'`
	} else {
		where = append(where, `m.account=? AND m.folder=?`)
		args = append(args, q.Account, q.Folder)
	}
	if q.BeforeDate != 0 || q.BeforeID != 0 {
		where = append(where, `(m.date<? OR (m.date=? AND m.id<?))`)
		args = append(args, q.BeforeDate, q.BeforeDate, q.BeforeID)
	}
	sqlq := `SELECT ` + msgCols + ` FROM ` + from
	if len(where) > 0 {
		sqlq += ` WHERE ` + strings.Join(where, " AND ")
	}
	sqlq += ` ORDER BY m.date DESC, m.id DESC LIMIT ?`
	args = append(args, q.Limit)
	rows, err := c.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one message.
func (c *Cache) Get(id int64) (Message, error) {
	return scanMessage(c.db.QueryRow(`SELECT `+msgCols+` FROM messages m WHERE m.id=?`, id))
}

// SetSeen marks cached messages read or unread, adjusting the folders'
// unread counts to match until the next server refresh.
func (c *Cache) SetSeen(ids []int64, seen bool) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var account, folder string
		var was bool
		if err := tx.QueryRow(`SELECT account, folder, seen FROM messages WHERE id=?`, id).Scan(&account, &folder, &was); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if was == seen {
			continue
		}
		delta := 1
		if seen {
			delta = -1
		}
		if _, err := tx.Exec(`UPDATE messages SET seen=? WHERE id=?`, seen, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE folders SET unread=MAX(0, unread+?) WHERE account=? AND id=?`, delta, account, folder); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveBody stores a message's source, and its snippet if it had none.
func (c *Cache) SaveBody(id int64, raw []byte, snippet string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO bodies(id, raw, fetched) VALUES(?,?,?)`, id, raw, time.Now().Unix()); err != nil {
		return err
	}
	if snippet != "" {
		if _, err := tx.Exec(`UPDATE messages SET snippet=? WHERE id=? AND snippet=''`, snippet, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Body returns a cached message source.
func (c *Cache) Body(id int64) ([]byte, bool, error) {
	var raw []byte
	err := c.db.QueryRow(`SELECT raw FROM bodies WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}
