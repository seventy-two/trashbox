// Package mailbox is what every account type (Outlook via Graph, Gmail via
// IMAP) offers the engine.
package mailbox

import (
	"context"
	"errors"
	"time"
)

// Folder roles.
const (
	RoleInbox   = "inbox"
	RoleSent    = "sent"
	RoleDrafts  = "drafts"
	RoleTrash   = "trash"
	RoleJunk    = "junk"
	RoleArchive = "archive"
	RoleAll     = "all"
	RoleFlagged = "flagged"
)

// Folder is a mail folder (an IMAP mailbox or a Graph mailFolder).
type Folder struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	Role   string `json:"role,omitempty"`
	Unread int    `json:"unread"`
	Total  int    `json:"total"`
}

// Header is what the message list needs about a message.
type Header struct {
	RID      string // the provider's ID within the folder
	Date     time.Time
	FromName string
	FromAddr string
	To       string // display text
	Subject  string
	Snippet  string // may be empty until the body is fetched
	Seen     bool
	Flagged  bool
	Attach   bool
}

// Flags is a flag-only change.
type Flags struct {
	RID     string
	Seen    bool
	Flagged bool
}

// Delta is what changed in a folder since the last sync.
type Delta struct {
	New     []Header // new messages, or changed ones sent in full
	Flags   []Flags
	Removed []string
	// Reset means New is everything in the window: drop local messages in
	// the window that aren't in it.
	Reset bool
	State string // pass back on the next Sync
}

// Provider is one account.
type Provider interface {
	Folders(ctx context.Context) ([]Folder, error)
	// Sync brings a folder's messages since `since` up to date. known is
	// what the cache holds in that window (RID → date).
	Sync(ctx context.Context, folder, state string, since time.Time, known map[string]time.Time) (Delta, error)
	// Older returns up to n messages received before `before`, newest first.
	Older(ctx context.Context, folder string, before time.Time, n int) (hs []Header, more bool, err error)
	// Raw returns the whole message (RFC 5322), without marking it read.
	Raw(ctx context.Context, folder, rid string) ([]byte, error)
	SetSeen(ctx context.Context, folder string, rids []string, seen bool) error
	// Watch blocks until ctx ends, calling changed when the folder may have
	// changed. Providers without push return ErrNoPush at once.
	Watch(ctx context.Context, folder string, changed func()) error
	Close() error
}

// Found is a message a server search turned up, and the folder it's in.
type Found struct {
	Folder string
	Header
}

// Searcher is a Provider that can search the whole account on the server.
// folder is where to look for providers that search one folder at a time
// (IMAP: Gmail's All Mail); others ignore it. Results come newest first.
type Searcher interface {
	Search(ctx context.Context, folder, q string, n int) ([]Found, error)
}

// ErrNoPush means the provider can't tell us about changes: poll instead.
var ErrNoPush = errors.New("no push for this account type")
