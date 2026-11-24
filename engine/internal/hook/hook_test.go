package hook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostRetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(502)
			return
		}
		var c Code
		json.NewDecoder(r.Body).Decode(&c)
		if c.Code != "482913" || c.From != "Bank" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("got %+v", c)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	p := Poster{URL: srv.URL + "/api/hooks/codes/SECRET", Wait: time.Millisecond}
	if err := p.Post(context.Background(), Code{From: "Bank", Code: "482913", Account: "a@x"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls %d", calls.Load())
	}
}

func TestPostGivesUpOnRefusalWithoutLeakingURL(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	err := Poster{URL: srv.URL + "/hooks/codes/SECRET", Wait: time.Millisecond}.Post(context.Background(), Code{Code: "1234"})
	if err == nil || strings.Contains(err.Error(), "SECRET") || calls.Load() != 1 {
		t.Fatalf("err %v, calls %d", err, calls.Load())
	}
	err = Poster{URL: "http://127.0.0.1:1/hooks/codes/SECRET", Wait: time.Millisecond, Tries: 2}.Post(context.Background(), Code{Code: "1234"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unreachable: %v", err)
	}
}
