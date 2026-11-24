package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/compose"
	"github.com/seventy-two/trashbox/engine/internal/engine"
	"github.com/seventy-two/trashbox/engine/internal/gmail"
	"github.com/seventy-two/trashbox/engine/internal/graph"
	"github.com/seventy-two/trashbox/engine/internal/imapmail"
	"github.com/seventy-two/trashbox/engine/internal/mailbox"
	"github.com/seventy-two/trashbox/engine/internal/msauth"
	"github.com/seventy-two/trashbox/engine/internal/store"
)

// tokenSource hands out Graph access tokens for one account, refreshing them
// and saving each rotated refresh token.
type tokenSource struct {
	ms  msauth.Config
	st  *store.Store
	acc store.Account

	mu  sync.Mutex
	tok msauth.Token
}

func (t *tokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tok.AccessToken != "" && time.Until(t.tok.Expiry) > 2*time.Minute {
		return t.tok.AccessToken, nil
	}
	refresh, err := t.st.Secret(t.acc)
	if err != nil {
		return "", fmt.Errorf("no saved sign-in: %w", err)
	}
	tok, err := t.ms.Refresh(ctx, refresh)
	if err != nil {
		return "", fmt.Errorf("%w (sign in again)", err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if err := t.st.SetSecret(t.acc, tok.RefreshToken); err != nil {
			return "", err
		}
	}
	t.tok = tok
	return tok.AccessToken, nil
}

// providers builds each configured account. Accounts that can't be built
// (a missing secret) are logged and skipped.
func providers(st *store.Store, cfg store.Config, logger *log.Logger) []engine.Account {
	var out []engine.Account
	for _, a := range cfg.Accounts {
		var p mailbox.Provider
		var send func(context.Context, compose.Outgoing) error
		switch a.Kind {
		case store.Outlook:
			ms, err := msConfig(cfg)
			if err != nil {
				logger.Printf("%s: %v", a.Address, err)
				continue
			}
			ts := &tokenSource{ms: ms, st: st, acc: a}
			gc := &graph.Client{Tokens: ts.Token}
			p = &graph.Mailbox{C: gc}
			// Graph reads the recipients from the message, so Bcc goes in it
			// (Exchange takes it out again before delivery).
			send = func(ctx context.Context, m compose.Outgoing) error {
				return gc.SendMIME(ctx, append([]byte(m.BccHeader), m.Raw...))
			}
		case store.Gmail:
			pass, err := st.Secret(a)
			if err != nil {
				logger.Printf("%s: no app password: %v", a.Address, err)
				continue
			}
			p = &imapmail.Mailbox{Addr: "imap.gmail.com:993", User: a.Address, Pass: pass}
			user := a.Address
			send = func(_ context.Context, m compose.Outgoing) error {
				return gmail.Default.SendRaw(user, pass, m.Rcpts, m.Raw)
			}
		default:
			logger.Printf("%s: unknown account kind %q", a.Address, a.Kind)
			continue
		}
		out = append(out, engine.Account{ID: a.Address, Kind: a.Kind, Provider: p, Send: send})
	}
	return out
}
