package gmail

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func imapServer(t *testing.T) string {
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("me@gmail.com", "abcdabcdabcdabcd")
	u.Create("INBOX", nil)
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	// Fill the inbox through the protocol, as a real server would be.
	c, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("me@gmail.com", "abcdabcdabcdabcd").Wait(); err != nil {
		t.Fatal(err)
	}
	for i, subj := range []string{"First", "Second", "Third"} {
		msg := fmt.Sprintf("From: Sender %d <s%d@example.com>\r\nSubject: %s\r\nDate: Mon, 2%d Nov 2026 10:00:00 +0000\r\n\r\nbody\r\n", i, i, subj, i)
		opts := &imap.AppendOptions{}
		if i == 0 {
			opts.Flags = []imap.Flag{imap.FlagSeen}
		}
		cmd := c.Append("INBOX", int64(len(msg)), opts)
		cmd.Write([]byte(msg))
		cmd.Close()
		if _, err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	return ln.Addr().String()
}

func TestCheck(t *testing.T) {
	s := Servers{IMAP: imapServer(t), Insecure: true}
	sum, err := s.Check("me@gmail.com", "abcdabcdabcdabcd", 2)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Messages != 3 || sum.Unseen != 2 {
		t.Fatalf("counts %+v", sum)
	}
	if len(sum.Recent) != 2 || sum.Recent[0].Subject != "Third" || sum.Recent[1].Subject != "Second" {
		t.Fatalf("recent %+v", sum.Recent)
	}
	if sum.Recent[0].From != "Sender 2" || sum.Recent[0].Seen {
		t.Fatalf("first %+v", sum.Recent[0])
	}
}

func TestCheckWrongPassword(t *testing.T) {
	s := Servers{IMAP: imapServer(t), Insecure: true}
	if _, err := s.Check("me@gmail.com", "nope", 2); err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("want login error, got %v", err)
	}
}

func TestNormalisePassword(t *testing.T) {
	if got := NormalisePassword(" abcd efgh ijkl mnop "); got != "abcdefghijklmnop" {
		t.Fatal(got)
	}
}

func TestBuildText(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	m := string(BuildText("me@gmail.com", []string{"a@x", "b@x"}, "Café test", "line 1\nline 2", now))
	for _, want := range []string{
		"From: me@gmail.com\r\n", "To: a@x, b@x\r\n", "Subject: =?utf-8?q?Caf=C3=A9_test?=\r\n",
		"Date: Tue, 24 Nov 2026 12:00:00 +0000\r\n", "@gmail.com>\r\n", "\r\n\r\nline 1\r\nline 2\r\n",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in\n%s", want, m)
		}
	}
}

// fakeSMTP accepts one message with AUTH PLAIN and records what it got.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { fmt.Fprint(conn, s+"\r\n") }
		var log strings.Builder
		say("220 fake")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.TrimSpace(line)
			log.WriteString(cmd + "\n")
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				say("250-fake")
				say("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				say("235 ok")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				say("250 ok")
			case cmd == "DATA":
				say("354 go")
				for {
					l, _ := r.ReadString('\n')
					if l == ".\r\n" {
						break
					}
					log.WriteString(l)
				}
				say("250 queued")
			case cmd == "QUIT":
				say("221 bye")
				got <- log.String()
				return
			default:
				say("502 ?")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSendText(t *testing.T) {
	addr, got := fakeSMTP(t)
	s := Servers{SMTP: addr, Insecure: true}
	if err := s.SendText("me@gmail.com", "pw", []string{"me@gmail.com"}, "trashbox test", "hello"); err != nil {
		t.Fatal(err)
	}
	log := <-got
	for _, want := range []string{"AUTH PLAIN", "MAIL FROM:<me@gmail.com>", "RCPT TO:<me@gmail.com>", "Subject: trashbox test", "hello"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
}

func TestSendRawBccOnlyInEnvelope(t *testing.T) {
	addr, got := fakeSMTP(t)
	s := Servers{SMTP: addr, Insecure: true}
	raw := []byte("From: <me@gmail.com>\r\nTo: <a@x.com>\r\nSubject: hi\r\n\r\nhello\r\n")
	if err := s.SendRaw("me@gmail.com", "pw", []string{"a@x.com", "hidden@x.com"}, raw); err != nil {
		t.Fatal(err)
	}
	log := <-got
	if !strings.Contains(log, "RCPT TO:<hidden@x.com>") || strings.Count(log, "hidden@") != 1 {
		t.Errorf("envelope:\n%s", log)
	}
}
