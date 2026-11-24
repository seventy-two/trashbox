package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fake(t *testing.T, h http.HandlerFunc) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return &Client{Token: "tok", Base: srv.URL}
}

func TestMeFallsBackToUPN(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"mail":null,"userPrincipalName":"Someone@Outlook.com"}`)
	})
	got, err := c.Me(context.Background())
	if err != nil || got != "someone@outlook.com" {
		t.Fatalf("Me = %q, %v", got, err)
	}
}

func TestRecent(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me/mailFolders/inbox/messages" || r.URL.Query().Get("$top") != "3" {
			t.Errorf("url %v", r.URL)
		}
		fmt.Fprint(w, `{"value":[
			{"subject":"Hi","isRead":false,"receivedDateTime":"2026-11-24T09:00:00Z","from":{"emailAddress":{"name":"Ann","address":"a@x"}}},
			{"subject":"Yo","isRead":true,"receivedDateTime":"2026-11-23T09:00:00Z","from":{"emailAddress":{"name":"","address":"b@x"}}}]}`)
	})
	got, err := c.Recent(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].From != "Ann" || got[1].From != "b@x" || got[0].Read || !got[1].Read {
		t.Fatalf("%+v", got)
	}
}

func TestSendText(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/me/sendMail" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Message struct {
				Subject string
				Body    struct{ ContentType, Content string }
				To      []struct {
					EmailAddress struct{ Address string } `json:"emailAddress"`
				} `json:"toRecipients"`
			} `json:"message"`
			Save bool `json:"saveToSentItems"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		m := body.Message
		if m.Subject != "S" || m.Body.ContentType != "Text" || m.Body.Content != "B" ||
			len(m.To) != 1 || m.To[0].EmailAddress.Address != "z@x" || !body.Save {
			t.Errorf("body %+v", body)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	if err := c.SendText(context.Background(), []string{"z@x"}, "S", "B"); err != nil {
		t.Fatal(err)
	}
}

func TestErrorDecoded(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"code":"ErrorAccessDenied","message":"Access is denied."}}`)
	})
	_, err := c.Inbox(context.Background())
	var ge *Error
	if !errors.As(err, &ge) || ge.Status != 403 || ge.Code != "ErrorAccessDenied" {
		t.Fatalf("err %v", err)
	}
}
