// Package graph is a small Microsoft Graph mail client: who am I, inbox
// counts, recent messages and sending.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBase is the Graph v1.0 endpoint.
const DefaultBase = "https://graph.microsoft.com/v1.0"

// Client calls Graph for one account.
type Client struct {
	Token string // a fixed access token, or
	// Tokens returns a current access token, refreshing it when needed.
	Tokens func(ctx context.Context) (string, error)
	Base   string       // defaults to DefaultBase
	HTTP   *http.Client // defaults to http.DefaultClient
}

// Error is a Graph API error response.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("graph: %d %s: %s", e.Status, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.send(ctx, method, path, body, func(r io.Reader) error {
		if out == nil {
			return nil
		}
		return json.NewDecoder(r).Decode(out)
	})
}

// doRaw is do for responses that aren't JSON.
func (c *Client) doRaw(ctx context.Context, method, path string, read func(io.Reader) error) error {
	return c.send(ctx, method, path, nil, read)
}

// send makes one request. path is relative to Base, or an absolute URL such
// as an @odata.nextLink.
func (c *Client) send(ctx context.Context, method, path string, body io.Reader, read func(io.Reader) error) error {
	u := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		base := c.Base
		if base == "" {
			base = DefaultBase
		}
		u = base + path
	}
	token := c.Token
	if c.Tokens != nil {
		var err error
		if token, err = c.Tokens(ctx); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Immutable IDs survive moves between folders; 200 a page keeps the
	// first sync of a big inbox to a sensible number of requests.
	req.Header.Add("Prefer", `IdType="ImmutableId"`)
	req.Header.Add("Prefer", "odata.maxpagesize=200")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return &Error{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	return read(resp.Body)
}

// Me returns the signed-in account's email address.
func (c *Client) Me(ctx context.Context) (string, error) {
	var me struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := c.do(ctx, http.MethodGet, "/me?$select=mail,userPrincipalName", nil, &me); err != nil {
		return "", err
	}
	if me.Mail != "" {
		return strings.ToLower(me.Mail), nil
	}
	return strings.ToLower(me.UserPrincipalName), nil
}

// FolderCounts is a folder's message counts.
type FolderCounts struct {
	Total  int `json:"totalItemCount"`
	Unread int `json:"unreadItemCount"`
}

// Inbox returns the inbox's counts.
func (c *Client) Inbox(ctx context.Context) (FolderCounts, error) {
	var f FolderCounts
	err := c.do(ctx, http.MethodGet, "/me/mailFolders/inbox?$select=totalItemCount,unreadItemCount", nil, &f)
	return f, err
}

// Summary is one message in a list.
type Summary struct {
	Subject  string
	From     string
	Received time.Time
	Read     bool
}

// Recent returns the newest n messages in the inbox.
func (c *Client) Recent(ctx context.Context, n int) ([]Summary, error) {
	var page struct {
		Value []struct {
			Subject  string    `json:"subject"`
			Received time.Time `json:"receivedDateTime"`
			IsRead   bool      `json:"isRead"`
			From     struct {
				EmailAddress struct{ Name, Address string } `json:"emailAddress"`
			} `json:"from"`
		} `json:"value"`
	}
	q := url.Values{
		"$top":     {fmt.Sprint(n)},
		"$select":  {"subject,from,receivedDateTime,isRead"},
		"$orderby": {"receivedDateTime desc"},
	}
	if err := c.do(ctx, http.MethodGet, "/me/mailFolders/inbox/messages?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(page.Value))
	for _, m := range page.Value {
		from := m.From.EmailAddress.Name
		if from == "" {
			from = m.From.EmailAddress.Address
		}
		out = append(out, Summary{Subject: m.Subject, From: from, Received: m.Received, Read: m.IsRead})
	}
	return out, nil
}

// SendText sends a plain-text message and keeps a copy in Sent Items.
func (c *Client) SendText(ctx context.Context, to []string, subject, body string) error {
	type addr struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	}
	rcpts := make([]addr, len(to))
	for i, a := range to {
		rcpts[i].EmailAddress.Address = a
	}
	msg := map[string]any{
		"message": map[string]any{
			"subject":      subject,
			"body":         map[string]string{"contentType": "Text", "content": body},
			"toRecipients": rcpts,
		},
		"saveToSentItems": true,
	}
	return c.do(ctx, http.MethodPost, "/me/sendMail", msg, nil)
}
