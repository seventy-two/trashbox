package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seventy-two/trashbox/engine/internal/mailparse"
)

func orig() *mailparse.Message {
	return &mailparse.Message{
		From:       []mailparse.Address{{Name: "Priya Shah", Addr: "priya@example.org"}},
		To:         []mailparse.Address{{Name: "Me", Addr: "Me@Example.com"}, {Name: "Sam", Addr: "sam@example.net"}},
		Cc:         []mailparse.Address{{Name: "Tom, the elder", Addr: "tom@example.net"}, {Addr: "sam@example.net"}},
		Subject:    "Sunday roast?",
		Date:       time.Date(2026, 11, 22, 18, 5, 0, 0, time.Local),
		MessageID:  "abc@example.org",
		References: []string{"first@example.org"},
		Text:       "Are you up for Sunday?\r\n\r\n> earlier\r\nP\r\n",
	}
}

func TestReply(t *testing.T) {
	d := Reply(orig(), "me@example.com", false)
	if d.To != "Priya Shah <priya@example.org>" || d.Cc != "" {
		t.Errorf("to %q cc %q", d.To, d.Cc)
	}
	if d.Subject != "Re: Sunday roast?" || d.InReplyTo != "abc@example.org" {
		t.Errorf("subject %q inReplyTo %q", d.Subject, d.InReplyTo)
	}
	if strings.Join(d.References, " ") != "first@example.org abc@example.org" {
		t.Errorf("references %v", d.References)
	}
	want := "\n\nOn Sun 22 Nov 2026 at 18:05, Priya Shah wrote:\n> Are you up for Sunday?\n> \n>> earlier\n> P\n"
	if d.Body != want {
		t.Errorf("body %q\nwant %q", d.Body, want)
	}
}

func TestReplyAll(t *testing.T) {
	d := Reply(orig(), "me@example.com", true)
	if d.To != "Priya Shah <priya@example.org>" {
		t.Errorf("to %q", d.To)
	}
	// Not me, Sam once, Tom's name quoted for its comma.
	if d.Cc != `Sam <sam@example.net>, "Tom, the elder" <tom@example.net>` {
		t.Errorf("cc %q", d.Cc)
	}
}

func TestReplyToAndOwnMessage(t *testing.T) {
	o := orig()
	o.ReplyTo = []mailparse.Address{{Addr: "list@example.org"}}
	o.Subject = "RE: Sunday roast?"
	if d := Reply(o, "me@example.com", false); d.To != "list@example.org" || d.Subject != "RE: Sunday roast?" {
		t.Errorf("reply-to: %q %q", d.To, d.Subject)
	}
	// Replying to something we sent goes back to its recipients.
	o = orig()
	o.From = []mailparse.Address{{Addr: "me@example.com"}}
	if d := Reply(o, "me@example.com", false); d.To != "Me <Me@Example.com>, Sam <sam@example.net>" {
		t.Errorf("own message: %q", d.To)
	}
}

func TestParseList(t *testing.T) {
	l, err := ParseList(` a@x.com; Ann Lee <ann@y.com>, "Tom, T" <t@z.com> `)
	if err != nil || len(l) != 3 || l[1].Name != "Ann Lee" || l[2].Address != "t@z.com" {
		t.Fatalf("%v %v", l, err)
	}
	if _, err := ParseList("a@x.com, not an address"); err == nil || !strings.Contains(err.Error(), "not an address") {
		t.Errorf("want the bad one named, got %v", err)
	}
}

func TestBuild(t *testing.T) {
	d := Reply(orig(), "me@example.com", true)
	d.Bcc = "secret@example.com"
	d.Body = "Yes! Café at 1pm.\n" + strings.Repeat("long line ", 20) + "\n" + d.Body
	o, err := Build(d, time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.Rcpts, " ") != "priya@example.org sam@example.net tom@example.net secret@example.com" {
		t.Errorf("rcpts %v", o.Rcpts)
	}
	raw := string(o.Raw)
	if strings.Contains(raw, "secret@") || !strings.Contains(o.BccHeader, "secret@example.com") {
		t.Errorf("bcc leaked or missing:\n%s\n%s", raw, o.BccHeader)
	}
	for _, want := range []string{"From: <me@example.com>", "In-Reply-To: <abc@example.org>", "References: <first@example.org>\r\n <abc@example.org>", "Subject: Re: Sunday roast?"} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in\n%s", want, raw)
		}
	}
	// It reads back as sent.
	m, err := mailparse.Parse(o.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Text, "Yes! Café at 1pm.\r\n") || len(m.Cc) != 2 || m.Cc[1].Name != "Tom, the elder" {
		t.Errorf("parsed back: %q %v", m.Text, m.Cc)
	}
	for _, line := range strings.Split(raw, "\r\n") {
		if len(line) > 78 {
			t.Errorf("line too long: %q", line)
		}
	}
}

func TestBuildNeedsSomeone(t *testing.T) {
	if _, err := Build(Draft{From: "me@example.com", Subject: "hi"}, time.Now()); err == nil {
		t.Error("sent to no one")
	}
	if _, err := Build(Draft{To: "a@x.com"}, time.Now()); err == nil {
		t.Error("sent from no one")
	}
}

func TestBuildWithAttachments(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "Receipt é.pdf")
	data := []byte("%PDF-1.4 " + strings.Repeat("x", 200))
	os.WriteFile(pdf, data, 0o600)
	blob := filepath.Join(dir, "notes")
	os.WriteFile(blob, []byte("plain words"), 0o600)
	d := Draft{From: "me@example.com", To: "a@x.com", Subject: "files", Body: "Here they are.", Attachments: []string{pdf, blob}}
	o, err := Build(d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Files) != 2 || o.Files[0].Type != "application/pdf" || o.Files[1].Type != "text/plain" {
		t.Fatalf("files %+v", o.Files)
	}
	if strings.Contains(string(o.Text), "multipart") || !strings.Contains(string(o.Raw), "multipart/mixed") {
		t.Error("Text should be the plain message, Raw the multipart one")
	}
	m, err := mailparse.Parse(o.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Text, "Here they are.") || len(m.Attachments) != 2 || m.Attachments[0].Name != "Receipt é.pdf" {
		t.Fatalf("parsed back: %q %+v", m.Text, m.Attachments)
	}
	if got, _ := m.AttachmentData(0); string(got) != string(data) {
		t.Error("the PDF didn't survive")
	}
	for _, line := range strings.Split(string(o.Raw), "\r\n") {
		if len(line) > 78 {
			t.Errorf("line too long: %q", line)
		}
	}
}

func TestBuildAttachmentProblems(t *testing.T) {
	d := Draft{From: "me@example.com", To: "a@x.com", Attachments: []string{"/no/such/file.txt"}}
	if _, err := Build(d, time.Now()); err == nil || !strings.Contains(err.Error(), "file.txt") {
		t.Errorf("missing file: %v", err)
	}
	d.Attachments = []string{t.TempDir()}
	if _, err := Build(d, time.Now()); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("folder: %v", err)
	}
}
