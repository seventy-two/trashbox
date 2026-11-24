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

// Client calls Graph with one account's access token.
type Client struct {
	Token string
	Base  string       // defaults to DefaultBase
	HTTP  *http.Client // defaults to http.DefaultClient
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
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if in != nil {
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
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
