package msauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCEChallengeIsS256OfVerifier(t *testing.T) {
	v, c := NewPKCE()
	sum := sha256.Sum256([]byte(v))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); c != want {
		t.Fatalf("challenge %q, want %q", c, want)
	}
	if len(v) < 43 {
		t.Fatalf("verifier too short: %d", len(v))
	}
}

func TestAuthURL(t *testing.T) {
	c := Config{ClientID: "cid", Scopes: []string{"a", "b"}}
	u, err := url.Parse(c.AuthURL("http://localhost:1234/", "st", "ch"))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != DefaultAuthority+"/oauth2/v2.0/authorize" {
		t.Errorf("endpoint %q", got)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id": "cid", "redirect_uri": "http://localhost:1234/", "scope": "a b",
		"state": "st", "code_challenge": "ch", "code_challenge_method": "S256", "response_type": "code",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func tokenServer(t *testing.T, check func(url.Values)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tenant/oauth2/v2.0/token" {
			t.Errorf("path %q", r.URL.Path)
		}
		r.ParseForm()
		check(r.PostForm)
		if r.PostForm.Get("refresh_token") == "bad" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"AADSTS70000: expired.\r\nTrace ID: x"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt2","expires_in":3600}`)
	}))
}

func TestExchangeAndRefresh(t *testing.T) {
	var seen []url.Values
	srv := tokenServer(t, func(v url.Values) { seen = append(seen, v) })
	defer srv.Close()
	c := Config{ClientID: "cid", Authority: srv.URL + "/tenant/", Scopes: MailScopes}

	tok, err := c.Exchange(context.Background(), "code1", "http://localhost:9/", "ver")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt2" || time.Until(tok.Expiry) < 59*time.Minute {
		t.Fatalf("token %+v", tok)
	}
	if v := seen[0]; v.Get("grant_type") != "authorization_code" || v.Get("code") != "code1" ||
		v.Get("code_verifier") != "ver" || v.Get("client_id") != "cid" || !strings.Contains(v.Get("scope"), "Mail.Send") {
		t.Errorf("exchange form %v", v)
	}

	if _, err := c.Refresh(context.Background(), "rt1"); err != nil {
		t.Fatal(err)
	}
	if v := seen[1]; v.Get("grant_type") != "refresh_token" || v.Get("refresh_token") != "rt1" {
		t.Errorf("refresh form %v", v)
	}

	_, err = c.Refresh(context.Background(), "bad")
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != "invalid_grant" {
		t.Fatalf("want invalid_grant, got %v", err)
	}
	if strings.Contains(err.Error(), "Trace") {
		t.Errorf("error should drop trace lines: %q", err)
	}
}

func TestLoginLoopback(t *testing.T) {
	srv := tokenServer(t, func(v url.Values) {
		if v.Get("code") != "the-code" {
			t.Errorf("code %q", v.Get("code"))
		}
	})
	defer srv.Close()
	c := Config{ClientID: "cid", Authority: srv.URL + "/tenant", Scopes: MailScopes}

	// Play the browser: follow the sign-in URL's redirect_uri with a code.
	open := func(signin string) error {
		u, _ := url.Parse(signin)
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(q.Get("state"))
		cb = strings.Replace(cb, "localhost", "127.0.0.1", 1)
		go func() {
			resp, err := http.Get(cb)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := c.Login(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "rt2" {
		t.Fatalf("token %+v", tok)
	}
}

func TestCallbackRejectsWrongState(t *testing.T) {
	var gotErr error
	h := callbackHandler("good", func(_ string, err error) { gotErr = err })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/?code=x&state=evil", nil))
	if gotErr == nil {
		t.Fatal("wrong state should fail")
	}
}
