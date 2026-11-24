// Package hook posts to kitchensink's hooks (sign-in codes, new mail), whose
// URLs (secret included) are stored encrypted like the other secrets.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Code is what kitchensink's codes monitor takes.
type Code struct {
	From    string `json:"from"`
	Code    string `json:"code"`
	Account string `json:"account"`
}

// Poster posts JSON to one URL.
type Poster struct {
	URL   string
	HTTP  *http.Client  // defaults to one with a 15 s timeout
	Tries int           // 4 by default
	Wait  time.Duration // between tries; 15 s by default
}

// Post sends v as JSON, trying again a few times if kitchensink can't be
// reached. Errors never include the URL, which holds the secret.
func (p Poster) Post(ctx context.Context, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	tries, wait := p.Tries, p.Wait
	if tries <= 0 {
		tries = 4
	}
	if wait <= 0 {
		wait = 15 * time.Second
	}
	var last error
	for i := 0; i < tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("hook: bad URL")
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			last = fmt.Errorf("hook: can't reach kitchensink")
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode < 300:
			return nil
		case resp.StatusCode >= 500:
			last = fmt.Errorf("hook: kitchensink said %d", resp.StatusCode)
		default:
			return fmt.Errorf("hook: kitchensink said %d", resp.StatusCode) // wrong secret etc.: retrying won't help
		}
	}
	return last
}
