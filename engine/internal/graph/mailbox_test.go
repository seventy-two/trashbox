package graph

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/mailbox"
)

func TestFoldersWithRolesAndChildren(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(strings.Join(r.Header.Values("Prefer"), ";"), "ImmutableId") {
			t.Errorf("no immutable id preference")
		}
		switch r.URL.Path {
		case "/me/mailFolders/inbox":
			fmt.Fprint(w, `{"id":"IN"}`)
		case "/me/mailFolders/sentitems":
			fmt.Fprint(w, `{"id":"SENT"}`)
		case "/me/mailFolders":
			if r.URL.Query().Get("page") == "" {
				fmt.Fprintf(w, `{"value":[{"id":"IN","displayName":"Inbox","parentFolderId":"ROOT","unreadItemCount":3,"totalItemCount":9,"childFolderCount":1}],
					"@odata.nextLink":"%s/me/mailFolders?page=2"}`, srvURL)
				return
			}
			fmt.Fprint(w, `{"value":[{"id":"SENT","displayName":"Sent Items","parentFolderId":"ROOT"}]}`)
		case "/me/mailFolders/IN/childFolders":
			fmt.Fprint(w, `{"value":[{"id":"KIDS","displayName":"Receipts","parentFolderId":"IN"}]}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"ErrorItemNotFound"}}`)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	b := &Mailbox{C: &Client{Token: "t", Base: srv.URL}}
	fs, err := b.Folders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []mailbox.Folder{
		{ID: "IN", Name: "Inbox", Role: "inbox", Unread: 3, Total: 9},
		{ID: "KIDS", Name: "Receipts", Parent: "IN"},
		{ID: "SENT", Name: "Sent Items", Role: "sent"},
	}
	if fmt.Sprint(fs) != fmt.Sprint(want) {
		t.Fatalf("got  %+v\nwant %+v", fs, want)
	}
}

func TestDeltaSync(t *testing.T) {
	var srvURL string
	expired := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/me/mailFolders/IN/messages/delta" && q.Get("$filter") != "":
			if !strings.HasPrefix(q.Get("$filter"), "receivedDateTime ge 2026-") {
				t.Errorf("filter %q", q.Get("$filter"))
			}
			fmt.Fprintf(w, `{"value":[
				{"id":"a","receivedDateTime":"2026-11-20T10:00:00Z","subject":"A","isRead":true,"bodyPreview":" hi ",
				 "from":{"emailAddress":{"name":"Ann","address":"Ann@X.com"}},"toRecipients":[{"emailAddress":{"name":"","address":"me@x"}}],
				 "flag":{"flagStatus":"flagged"},"hasAttachments":true}],
				"@odata.nextLink":"%s/me/mailFolders/IN/messages/delta?skip=1"}`, srvURL)
		case q.Get("skip") == "1":
			fmt.Fprintf(w, `{"value":[{"id":"b","receivedDateTime":"2026-11-21T10:00:00Z","subject":"B"}],
				"@odata.deltaLink":"%s/me/mailFolders/IN/messages/delta?token=1"}`, srvURL)
		case q.Get("token") == "1":
			if expired {
				w.WriteHeader(410)
				fmt.Fprint(w, `{"error":{"code":"SyncStateNotFound","message":"gone"}}`)
				return
			}
			fmt.Fprintf(w, `{"value":[
				{"id":"a","@removed":{"reason":"deleted"}},
				{"id":"b","isRead":true},
				{"id":"c","receivedDateTime":"2026-11-24T10:00:00Z","subject":"C"}],
				"@odata.deltaLink":"%s/me/mailFolders/IN/messages/delta?token=2"}`, srvURL)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	b := &Mailbox{C: &Client{Token: "t", Base: srv.URL}}
	ctx := context.Background()
	since := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

	d, err := b.Sync(ctx, "IN", "", since, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Reset || len(d.New) != 2 || !strings.HasSuffix(d.State, "token=1") {
		t.Fatalf("initial %+v", d)
	}
	a := d.New[0]
	if a.FromName != "Ann" || a.FromAddr != "ann@x.com" || !a.Seen || !a.Flagged || !a.Attach || a.Snippet != "hi" || a.To != "me@x" {
		t.Fatalf("header %+v", a)
	}

	d, err = b.Sync(ctx, "IN", d.State, since, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Reset || len(d.Removed) != 1 || d.Removed[0] != "a" || len(d.Flags) != 1 || !d.Flags[0].Seen ||
		len(d.New) != 1 || d.New[0].Subject != "C" || !strings.HasSuffix(d.State, "token=2") {
		t.Fatalf("delta %+v", d)
	}

	expired = true
	d, err = b.Sync(ctx, "IN", srvURL+"/me/mailFolders/IN/messages/delta?token=1", since, nil)
	if err != nil || !d.Reset || len(d.New) != 2 {
		t.Fatalf("expired token should restart: %+v %v", d, err)
	}
}

func TestOlderAndRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/mailFolders/IN/messages":
			q := r.URL.Query()
			if q.Get("$filter") != "receivedDateTime lt 2026-09-25T00:00:00Z" || q.Get("$top") != "2" || q.Get("$orderby") != "receivedDateTime desc" {
				t.Errorf("query %v", q)
			}
			fmt.Fprint(w, `{"value":[{"id":"x","receivedDateTime":"2026-09-01T00:00:00Z"},{"id":"y","receivedDateTime":"2026-08-01T00:00:00Z"}]}`)
		case "/me/messages/x/$value":
			io.WriteString(w, "From: a@b\r\n\r\nraw body")
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	b := &Mailbox{C: &Client{Token: "t", Base: srv.URL}}
	ctx := context.Background()
	hs, more, err := b.Older(ctx, "IN", time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), 2)
	if err != nil || len(hs) != 2 || !more {
		t.Fatalf("older %+v %v %v", hs, more, err)
	}
	raw, err := b.Raw(ctx, "IN", "x")
	if err != nil || !strings.HasSuffix(string(raw), "raw body") {
		t.Fatalf("raw %q %v", raw, err)
	}
}

func TestTokensFuncUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"mail":"a@b"}`)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Tokens: func(context.Context) (string, error) { return "fresh", nil }}
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
}
