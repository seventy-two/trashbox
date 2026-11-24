package mailparse

import (
	"strings"
	"testing"
)

const multipart = "From: \"Ann Example\" <ann@example.com>\r\n" +
	"To: me@example.com, Bob <bob@example.com>\r\n" +
	"Subject: =?iso-8859-1?q?Caf=E9_menu?=\r\n" +
	"Date: Tue, 24 Nov 2026 10:00:00 +0000\r\n" +
	"Message-ID: <abc@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/related; boundary=rel\r\n\r\n" +
	"--rel\r\n" +
	"Content-Type: multipart/alternative; boundary=alt\r\n\r\n" +
	"--alt\r\n" +
	"Content-Type: text/plain; charset=iso-8859-1\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Caf=E9 menu\r\n" +
	"--alt\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n\r\n" +
	"<p>Café <img src=\"cid:logo@x\"></p>\r\n" +
	"--alt--\r\n" +
	"--rel\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-ID: <logo@x>\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"iVBORw0KGgo=\r\n" +
	"--rel--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"menu.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"JVBERi0=\r\n" +
	"--outer--\r\n"

func TestParseMultipart(t *testing.T) {
	m, err := Parse([]byte(multipart))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Café menu" {
		t.Errorf("subject %q", m.Subject)
	}
	if len(m.From) != 1 || m.From[0].Name != "Ann Example" || m.From[0].Addr != "ann@example.com" {
		t.Errorf("from %+v", m.From)
	}
	if len(m.To) != 2 || m.To[1].Name != "Bob" {
		t.Errorf("to %+v", m.To)
	}
	if strings.TrimSpace(m.Text) != "Café menu" {
		t.Errorf("text %q", m.Text)
	}
	if !strings.Contains(m.HTML, "cid:logo@x") {
		t.Errorf("html %q", m.HTML)
	}
	if r, ok := m.Inline["logo@x"]; !ok || r.Type != "image/png" || len(r.Data) != 8 {
		t.Errorf("inline %+v", m.Inline)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "menu.pdf" || m.Attachments[0].Size != 5 {
		t.Errorf("attachments %+v", m.Attachments)
	}
	if d, ok := m.AttachmentData(0); !ok || string(d) != "%PDF-" {
		t.Errorf("attachment data %q", d)
	}
	if m.MessageID != "abc@example.com" {
		t.Errorf("message id %q", m.MessageID)
	}
}

func TestParsePlainOnly(t *testing.T) {
	m, err := Parse([]byte("From: x@y\r\nSubject: hi\r\n\r\nhello\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.HTML != "" || strings.TrimSpace(m.Text) != "hello" {
		t.Fatalf("%+v", m)
	}
}

func TestParseBadFromKeepsRaw(t *testing.T) {
	m, err := Parse([]byte("From: not an address\r\n\r\nx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.From) != 1 || m.From[0].Addr != "not an address" {
		t.Fatalf("%+v", m.From)
	}
}
