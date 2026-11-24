// Package msauth signs in to Microsoft accounts with OAuth 2.0 (authorisation
// code + PKCE, loopback redirect) and refreshes the tokens it gets back.
//
// It is written for a public desktop client: no client secret, and the
// redirect is http://localhost:<port>/, which Entra accepts for any port when
// the app registration lists plain "http://localhost".
package msauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAuthority accepts both personal Microsoft accounts and work/school
// accounts, so it works whichever kind each Outlook address is.
const DefaultAuthority = "https://login.microsoftonline.com/common"

// MailScopes are the Graph permissions trashbox asks for.
var MailScopes = []string{"offline_access", "User.Read", "Mail.ReadWrite", "Mail.Send"}

// Config describes the app registration and where to sign in.
type Config struct {
	ClientID  string
	Authority string // defaults to DefaultAuthority
	Scopes    []string
	HTTP      *http.Client // defaults to http.DefaultClient
}

// Token is what the token endpoint hands back.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

func (c Config) authority() string {
	if c.Authority != "" {
		return strings.TrimRight(c.Authority, "/")
	}
	return DefaultAuthority
}

func (c Config) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// NewPKCE returns a random code verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	verifier = randomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthURL is the page the user signs in on.
func (c Config) AuthURL(redirect, state, challenge string) string {
	q := url.Values{
		"client_id":             {c.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"response_mode":         {"query"},
		"scope":                 {strings.Join(c.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return c.authority() + "/oauth2/v2.0/authorize?" + q.Encode()
}

// Exchange swaps an authorisation code for tokens.
func (c Config) Exchange(ctx context.Context, code, redirect, verifier string) (Token, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	})
}

// Refresh gets a new access token. Microsoft rotates refresh tokens, so the
// caller must store the RefreshToken of the result.
func (c Config) Refresh(ctx context.Context, refresh string) (Token, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
}

// Error is an OAuth error from the token endpoint.
type Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *Error) Error() string {
	d := e.Description
	if i := strings.IndexAny(d, "\r\n"); i >= 0 {
		d = d[:i] // the rest is trace IDs and timestamps
	}
	return "microsoft sign-in: " + e.Code + ": " + d
}

func (c Config) token(ctx context.Context, form url.Values) (Token, error) {
	form.Set("client_id", c.ClientID)
	form.Set("scope", strings.Join(c.Scopes, " "))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.authority()+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client().Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	var body struct {
		Error
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Token{}, fmt.Errorf("token endpoint: %s: %w", resp.Status, err)
	}
	if body.Code != "" {
		e := body.Error
		return Token{}, &e
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return Token{}, fmt.Errorf("token endpoint: %s", resp.Status)
	}
	return Token{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(body.ExpiresIn) * time.Second),
	}, nil
}

// Login runs the whole browser sign-in: it listens on a loopback port, calls
// open with the sign-in URL, waits for the redirect and exchanges the code.
func (c Config) Login(ctx context.Context, open func(string) error) (Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	listeners := []net.Listener{ln}
	// Browsers may resolve localhost to ::1 first; listen there too if we can.
	if ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
		listeners = append(listeners, ln6)
	}

	redirect := fmt.Sprintf("http://localhost:%d/", port)
	verifier, challenge := NewPKCE()
	state := randomString(16)

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{Handler: callbackHandler(state, func(code string, err error) {
		select {
		case done <- result{code, err}:
		default:
		}
	})}
	for _, l := range listeners {
		go srv.Serve(l)
	}
	defer srv.Close()

	if err := open(c.AuthURL(redirect, state, challenge)); err != nil {
		return Token{}, fmt.Errorf("opening the browser: %w", err)
	}

	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return Token{}, r.err
		}
		return c.Exchange(ctx, r.code, redirect, verifier)
	}
}

// callbackHandler handles the redirect back from the sign-in page.
func callbackHandler(state string, report func(code string, err error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var msg string
		switch {
		case q.Get("error") != "":
			msg = "Sign-in failed: " + q.Get("error_description")
			report("", &Error{Code: q.Get("error"), Description: q.Get("error_description")})
		case q.Get("state") != state:
			msg = "Sign-in failed: the reply didn't match this request."
			report("", errors.New("microsoft sign-in: state mismatch"))
		case q.Get("code") == "":
			msg = "Sign-in failed: no code came back."
			report("", errors.New("microsoft sign-in: no code in redirect"))
		default:
			msg = "Signed in. You can close this tab and go back to trashbox."
			report(q.Get("code"), nil)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>trashbox</title>`+
			`<body style="font:16px system-ui,sans-serif;margin:3rem">%s</body>`, html.EscapeString(msg))
	})
}
