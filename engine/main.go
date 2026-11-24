// Command trashbox-engine is the mail half of trashbox. For now it only has
// the spike commands that prove each provider can sign in, read and send.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/gmail"
	"github.com/seventy-two/trashbox/engine/internal/graph"
	"github.com/seventy-two/trashbox/engine/internal/msauth"
	"github.com/seventy-two/trashbox/engine/internal/store"
	"github.com/seventy-two/trashbox/engine/internal/winui"
)

const usage = `trashbox-engine spike <command> [flags]

  accounts                        list accounts
  set-client-id <id>              save the Entra app's Application (client) ID
  outlook-login                   sign in an Outlook account in the browser
  outlook-check [-account a]      inbox counts and the newest messages
  outlook-send  [-account a] [-to b]   send a test email (to yourself by default)
  gmail-add                       add a Gmail account with an app password
  gmail-check   [-account a]
  gmail-send    [-account a] [-to b]
`

func main() {
	if len(os.Args) < 3 || os.Args[1] != "spike" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := spike(os.Args[2], os.Args[3:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func spike(cmd string, args []string) error {
	st, err := store.Open()
	if err != nil {
		return err
	}
	cfg, err := st.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	account := fs.String("account", "", "account address")
	to := fs.String("to", "", "recipient (default: the account itself)")
	fs.Parse(args)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch cmd {
	case "accounts":
		fmt.Println("data:", st.Dir)
		for _, a := range cfg.Accounts {
			fmt.Printf("  %-8s %s\n", a.Kind, a.Address)
		}
		return nil

	case "set-client-id":
		if fs.NArg() != 1 {
			return errors.New("usage: set-client-id <application id>")
		}
		cfg.OutlookClientID = strings.TrimSpace(fs.Arg(0))
		return st.Save(cfg)

	case "outlook-login":
		ms, err := msConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Println("Opening the Microsoft sign-in page in your browser…")
		tok, err := ms.Login(ctx, winui.OpenURL)
		if err != nil {
			return err
		}
		addr, err := (&graph.Client{Token: tok.AccessToken}).Me(ctx)
		if err != nil {
			return err
		}
		a := store.Account{Kind: store.Outlook, Address: addr}
		if err := st.SetSecret(a, tok.RefreshToken); err != nil {
			return err
		}
		cfg.Add(a)
		fmt.Println("Signed in:", addr)
		return st.Save(cfg)

	case "outlook-check", "outlook-send":
		a, err := cfg.Find(store.Outlook, *account)
		if err != nil {
			return err
		}
		gc, err := outlookClient(ctx, st, cfg, a)
		if err != nil {
			return err
		}
		if cmd == "outlook-send" {
			return sendTest(a.Address, *to, func(rcpt, subj, body string) error {
				return gc.SendText(ctx, []string{rcpt}, subj, body)
			})
		}
		counts, err := gc.Inbox(ctx)
		if err != nil {
			return err
		}
		recent, err := gc.Recent(ctx, 5)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d in inbox, %d unread\n", a.Address, counts.Total, counts.Unread)
		for _, m := range recent {
			printMsg(m.Received, m.Read, m.From, m.Subject)
		}
		return nil

	case "gmail-add":
		user, pass, err := winui.PromptCredentials("trashbox: add Gmail",
			"Your Gmail address, and an app password from myaccount.google.com/apppasswords (not your normal password).", "")
		if err != nil {
			return err
		}
		user = strings.ToLower(strings.TrimSpace(user))
		pass = gmail.NormalisePassword(pass)
		if !strings.Contains(user, "@") {
			user += "@gmail.com"
		}
		if _, err := gmail.Default.Check(user, pass, 0); err != nil {
			return fmt.Errorf("%s didn't sign in, nothing saved: %w", user, err)
		}
		a := store.Account{Kind: store.Gmail, Address: user}
		if err := st.SetSecret(a, pass); err != nil {
			return err
		}
		cfg.Add(a)
		fmt.Println("Added:", user)
		return st.Save(cfg)

	case "gmail-check", "gmail-send":
		a, err := cfg.Find(store.Gmail, *account)
		if err != nil {
			return err
		}
		pass, err := st.Secret(a)
		if err != nil {
			return err
		}
		if cmd == "gmail-send" {
			return sendTest(a.Address, *to, func(rcpt, subj, body string) error {
				return gmail.Default.SendText(a.Address, pass, []string{rcpt}, subj, body)
			})
		}
		sum, err := gmail.Default.Check(a.Address, pass, 5)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d in inbox, %d unread\n", a.Address, sum.Messages, sum.Unseen)
		for _, m := range sum.Recent {
			printMsg(m.Date, m.Seen, m.From, m.Subject)
		}
		return nil
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

func msConfig(cfg store.Config) (msauth.Config, error) {
	if cfg.OutlookClientID == "" {
		return msauth.Config{}, errors.New("no client ID yet: run set-client-id first")
	}
	return msauth.Config{ClientID: cfg.OutlookClientID, Scopes: msauth.MailScopes}, nil
}

// outlookClient refreshes the account's tokens (saving the rotated refresh
// token) and returns a Graph client.
func outlookClient(ctx context.Context, st *store.Store, cfg store.Config, a store.Account) (*graph.Client, error) {
	ms, err := msConfig(cfg)
	if err != nil {
		return nil, err
	}
	refresh, err := st.Secret(a)
	if err != nil {
		return nil, err
	}
	tok, err := ms.Refresh(ctx, refresh)
	if err != nil {
		return nil, fmt.Errorf("%w (run outlook-login again)", err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if err := st.SetSecret(a, tok.RefreshToken); err != nil {
			return nil, err
		}
	}
	return &graph.Client{Token: tok.AccessToken}, nil
}

func sendTest(from, to string, send func(rcpt, subj, body string) error) error {
	if to == "" {
		to = from
	}
	subj := "trashbox test " + time.Now().Format("15:04:05")
	if err := send(to, subj, "Sent by the trashbox spike from "+from+".\n"); err != nil {
		return err
	}
	fmt.Printf("Sent %q from %s to %s\n", subj, from, to)
	return nil
}

func printMsg(when time.Time, read bool, from, subject string) {
	mark := "•"
	if read {
		mark = " "
	}
	if len([]rune(from)) > 24 {
		from = string([]rune(from)[:23]) + "…"
	}
	fmt.Printf("  %s %s  %-24s %s\n", mark, when.Local().Format("02 Jan 15:04"), from, subject)
}
