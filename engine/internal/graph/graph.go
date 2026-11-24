// Package graph is a small Microsoft Graph mail client: who am I, inbox
// counts, recent messages and sending.
package graph

import (
	"bytes"
	"context"
	"encoding/base64"
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
	return c.send(ctx, method, path, body, "application/json", func(r io.Reader) error {
		if out == nil {
			return nil
		}
		return json.NewDecoder(r).Decode(out)
	})
}

// doRaw is do for responses that aren't JSON.
func (c *Client) doRaw(ctx context.Context, method, path string, read func(io.Reader) error) error {
	return c.send(ctx, method, path, nil, "", read)
}

// send makes one request. path is relative to Base, or an absolute URL such
// as an @odata.nextLink.
func (c *Client) send(ctx context.Context, method, path string, body io.Reader, contentType string, read func(io.Reader) error) error {
	u := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		u = c.base() + path
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
		req.Header.Set("Content-Type", contentType)
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

// SendMIME sends a whole RFC 5322 message, recipients (Bcc included) taken
// from its headers, and keeps a copy in Sent Items.
func (c *Client) SendMIME(ctx context.Context, raw []byte) error {
	body := base64.StdEncoding.EncodeToString(raw)
	return c.send(ctx, http.MethodPost, "/me/sendMail", strings.NewReader(body), "text/plain", func(io.Reader) error { return nil })
}

// File is an attachment for SendLarge.
type File struct {
	Name, Type string
	Data       []byte
}

// mimeLimit is the most SendMIME sends in one request: Graph turns away
// requests over 4 MB, and the MIME goes base64-encoded.
const mimeLimit = 3 << 20

// smallAttach is the biggest file added in one request; bigger ones go up
// in chunks through an upload session.
const smallAttach = 3 << 20

// Send sends a message: in one go if it's small enough, otherwise as a
// draft (made from text, the message without its attachments) with the
// files added to it, then sent. Either way a copy goes to Sent Items.
func (c *Client) Send(ctx context.Context, raw, text []byte, files []File) error {
	if len(raw) <= mimeLimit || len(files) == 0 {
		return c.SendMIME(ctx, raw)
	}
	var draft struct {
		ID string `json:"id"`
	}
	err := c.send(ctx, http.MethodPost, "/me/messages", strings.NewReader(base64.StdEncoding.EncodeToString(text)), "text/plain",
		func(r io.Reader) error { return json.NewDecoder(r).Decode(&draft) })
	if err != nil {
		return fmt.Errorf("making the draft: %w", err)
	}
	if draft.ID == "" {
		return fmt.Errorf("making the draft: no id came back")
	}
	msg := "/me/messages/" + url.PathEscape(draft.ID)
	fail := func(err error) error {
		// Don't leave a half-made draft behind.
		ctx2, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		c.do(ctx2, http.MethodDelete, msg, nil, nil)
		return err
	}
	for _, f := range files {
		if err := c.attach(ctx, msg, f); err != nil {
			return fail(fmt.Errorf("attaching %s: %w", f.Name, err))
		}
	}
	if err := c.do(ctx, http.MethodPost, msg+"/send", nil, nil); err != nil {
		return fail(err)
	}
	return nil
}

func (c *Client) attach(ctx context.Context, msg string, f File) error {
	if len(f.Data) <= smallAttach {
		return c.do(ctx, http.MethodPost, msg+"/attachments", map[string]any{
			"@odata.type": "#microsoft.graph.fileAttachment", "name": f.Name, "contentType": f.Type,
			"contentBytes": base64.StdEncoding.EncodeToString(f.Data),
		}, nil)
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	err := c.do(ctx, http.MethodPost, msg+"/attachments/createUploadSession", map[string]any{
		"AttachmentItem": map[string]any{"attachmentType": "file", "name": f.Name, "size": len(f.Data), "contentType": f.Type},
	}, &sess)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(sess.UploadURL, "https://") && !strings.HasPrefix(sess.UploadURL, c.base()) {
		return fmt.Errorf("odd upload URL")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	const chunk = 16 * 320 << 10 // 5 MiB: chunks must be multiples of 320 KiB
	for at := 0; at < len(f.Data); at += chunk {
		end := min(at+chunk, len(f.Data))
		// The upload URL carries its own authorisation: no bearer token.
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, sess.UploadURL, bytes.NewReader(f.Data[at:end]))
		if err != nil {
			return err
		}
		req.ContentLength = int64(end - at)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", at, end-1, len(f.Data)))
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("upload: %d", resp.StatusCode)
		}
	}
	return nil
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return DefaultBase
}
