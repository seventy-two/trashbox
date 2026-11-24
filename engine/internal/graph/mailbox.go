package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

// Mailbox is an Outlook account as a mailbox.Provider.
type Mailbox struct {
	C *Client

	mu    sync.Mutex
	roles map[string]string // folder ID → role
}

var _ mailbox.Provider = (*Mailbox)(nil)

const headerSelect = "id,receivedDateTime,subject,from,toRecipients,bodyPreview,isRead,flag,hasAttachments"

type message struct {
	ID       string    `json:"id"`
	Received time.Time `json:"receivedDateTime"`
	Subject  string    `json:"subject"`
	Preview  string    `json:"bodyPreview"`
	IsRead   bool      `json:"isRead"`
	Attach   bool      `json:"hasAttachments"`
	From     *struct {
		EmailAddress struct{ Name, Address string } `json:"emailAddress"`
	} `json:"from"`
	To []struct {
		EmailAddress struct{ Name, Address string } `json:"emailAddress"`
	} `json:"toRecipients"`
	Flag struct {
		Status string `json:"flagStatus"`
	} `json:"flag"`
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

func (m message) header() mailbox.Header {
	h := mailbox.Header{
		RID: m.ID, Date: m.Received, Subject: m.Subject, Snippet: strings.TrimSpace(m.Preview),
		Seen: m.IsRead, Flagged: m.Flag.Status == "flagged", Attach: m.Attach,
	}
	if m.From != nil {
		h.FromName, h.FromAddr = m.From.EmailAddress.Name, strings.ToLower(m.From.EmailAddress.Address)
	}
	to := make([]string, 0, len(m.To))
	for _, r := range m.To {
		if r.EmailAddress.Name != "" {
			to = append(to, r.EmailAddress.Name)
		} else {
			to = append(to, r.EmailAddress.Address)
		}
	}
	h.To = strings.Join(to, ", ")
	return h
}

var wellKnown = map[string]string{
	"inbox": mailbox.RoleInbox, "sentitems": mailbox.RoleSent, "drafts": mailbox.RoleDrafts,
	"deleteditems": mailbox.RoleTrash, "junkemail": mailbox.RoleJunk, "archive": mailbox.RoleArchive,
}

func (b *Mailbox) folderRoles(ctx context.Context) map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.roles != nil {
		return b.roles
	}
	roles := map[string]string{}
	for name, role := range wellKnown {
		var f struct {
			ID string `json:"id"`
		}
		if err := b.C.do(ctx, http.MethodGet, "/me/mailFolders/"+name+"?$select=id", nil, &f); err == nil {
			roles[f.ID] = role
		}
	}
	if len(roles) > 0 {
		b.roles = roles
	}
	return roles
}

// Folders lists every folder, children included.
func (b *Mailbox) Folders(ctx context.Context) ([]mailbox.Folder, error) {
	roles := b.folderRoles(ctx)
	var out []mailbox.Folder
	var walk func(path string, depth int) error
	walk = func(path string, depth int) error {
		next := path + "?$top=100&$select=id,displayName,parentFolderId,unreadItemCount,totalItemCount,childFolderCount"
		for next != "" {
			var page struct {
				Value []struct {
					ID       string `json:"id"`
					Name     string `json:"displayName"`
					Parent   string `json:"parentFolderId"`
					Unread   int    `json:"unreadItemCount"`
					Total    int    `json:"totalItemCount"`
					Children int    `json:"childFolderCount"`
				} `json:"value"`
				Next string `json:"@odata.nextLink"`
			}
			if err := b.C.do(ctx, http.MethodGet, next, nil, &page); err != nil {
				return err
			}
			for _, f := range page.Value {
				parent := f.Parent
				if depth == 0 {
					parent = ""
				}
				out = append(out, mailbox.Folder{ID: f.ID, Name: f.Name, Parent: parent, Role: roles[f.ID], Unread: f.Unread, Total: f.Total})
				if f.Children > 0 && depth < 8 {
					if err := walk("/me/mailFolders/"+url.PathEscape(f.ID)+"/childFolders", depth+1); err != nil {
						return err
					}
				}
			}
			next = page.Next
		}
		return nil
	}
	return out, walk("/me/mailFolders", 0)
}

// Sync uses Graph's delta query: the first call lists the window, later ones
// (from the saved deltaLink) only what changed.
func (b *Mailbox) Sync(ctx context.Context, folder, state string, since time.Time, _ map[string]time.Time) (mailbox.Delta, error) {
	d := mailbox.Delta{}
	next := state
	if next == "" {
		d.Reset = true
		q := url.Values{
			"$select": {headerSelect},
			"$filter": {"receivedDateTime ge " + since.UTC().Format(time.RFC3339)},
		}
		next = "/me/mailFolders/" + url.PathEscape(folder) + "/messages/delta?" + q.Encode()
	}
	for {
		var page struct {
			Value []message `json:"value"`
			Next  string    `json:"@odata.nextLink"`
			Delta string    `json:"@odata.deltaLink"`
		}
		err := b.C.do(ctx, http.MethodGet, next, nil, &page)
		var ge *Error
		if errors.As(err, &ge) && state != "" && (ge.Status == http.StatusGone || ge.Code == "SyncStateNotFound" || ge.Code == "syncStateNotFound") {
			return b.Sync(ctx, folder, "", since, nil) // the delta token expired: start again
		}
		if err != nil {
			return d, err
		}
		for _, m := range page.Value {
			if m.Removed != nil {
				d.Removed = append(d.Removed, m.ID)
				continue
			}
			if m.Received.IsZero() {
				// A change notice carrying only some fields (e.g. isRead).
				d.Flags = append(d.Flags, mailbox.Flags{RID: m.ID, Seen: m.IsRead, Flagged: m.Flag.Status == "flagged"})
				continue
			}
			d.New = append(d.New, m.header())
		}
		if page.Delta != "" {
			d.State = page.Delta
			return d, nil
		}
		if page.Next == "" {
			return d, errors.New("graph delta: no next or delta link")
		}
		next = page.Next
	}
}

// Older lists messages received before `before`, newest first.
func (b *Mailbox) Older(ctx context.Context, folder string, before time.Time, n int) ([]mailbox.Header, bool, error) {
	q := url.Values{
		"$select":  {headerSelect},
		"$filter":  {"receivedDateTime lt " + before.UTC().Format(time.RFC3339)},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {fmt.Sprint(n)},
	}
	var page struct {
		Value []message `json:"value"`
	}
	if err := b.C.do(ctx, http.MethodGet, "/me/mailFolders/"+url.PathEscape(folder)+"/messages?"+q.Encode(), nil, &page); err != nil {
		return nil, false, err
	}
	out := make([]mailbox.Header, 0, len(page.Value))
	for _, m := range page.Value {
		out = append(out, m.header())
	}
	return out, len(page.Value) == n, nil
}

// Raw fetches the MIME source. Reading it doesn't mark the message read.
func (b *Mailbox) Raw(ctx context.Context, _ string, rid string) ([]byte, error) {
	var raw []byte
	err := b.C.doRaw(ctx, http.MethodGet, "/me/messages/"+url.PathEscape(rid)+"/$value", func(r io.Reader) error {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r, 150<<20))
		return err
	})
	return raw, err
}

// SetSeen marks messages read or unread.
func (b *Mailbox) SetSeen(ctx context.Context, _ string, rids []string, seen bool) error {
	for _, id := range rids {
		if err := b.C.do(ctx, http.MethodPatch, "/me/messages/"+url.PathEscape(id), map[string]bool{"isRead": seen}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Watch: Graph only pushes to public webhooks, so the engine polls.
func (b *Mailbox) Watch(context.Context, string, func()) error { return mailbox.ErrNoPush }

// Close does nothing: Graph is plain HTTP.
func (b *Mailbox) Close() error { return nil }
