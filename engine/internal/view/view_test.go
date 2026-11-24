package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seventy-two/trashbox/engine/internal/mailparse"
)

func TestWriteHTMLWithInlineAndRemote(t *testing.T) {
	dir := t.TempDir()
	m := &mailparse.Message{
		HTML: `<html><head><meta charset="iso-8859-1"><base href="http://evil/"></head>` +
			`<body><img src="CID:Logo@X"><img src="https://track.example/p.gif"></body></html>`,
		Inline: map[string]mailparse.Resource{"logo@x": {Type: "image/png", Data: []byte("png")}},
	}
	p, err := Write(dir, 42, m, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "https://trashbox.view/42/index.html" || !p.HasRemote || p.Remote {
		t.Fatalf("%+v", p)
	}
	b, _ := os.ReadFile(p.Path)
	doc := string(b)
	if !strings.HasPrefix(doc, `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy"`) {
		t.Errorf("head: %.120s", doc)
	}
	if strings.Contains(doc, "iso-8859-1") || strings.Contains(doc, "http://evil") {
		t.Errorf("email's charset/base should be stripped: %s", doc)
	}
	if !strings.Contains(doc, "img-src 'self' data:;") {
		t.Errorf("remote images should be blocked: %s", doc)
	}
	if !strings.Contains(doc, `src="cid-1.png"`) {
		t.Errorf("cid not rewritten: %s", doc)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "42", "cid-1.png")); err != nil || string(data) != "png" {
		t.Errorf("inline file: %q %v", data, err)
	}

	p, _ = Write(dir, 42, m, true)
	b, _ = os.ReadFile(p.Path)
	if !p.Remote || !strings.Contains(string(b), "img-src 'self' data: https: http:") {
		t.Errorf("remote allowed: %s", b)
	}
}

func TestWriteText(t *testing.T) {
	p, err := Write(t.TempDir(), 7, &mailparse.Message{Text: "a <b> & see https://example.com/x?y=1&z=2."}, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p.Path)
	doc := string(b)
	if !strings.Contains(doc, "a &lt;b&gt; &amp; see") {
		t.Errorf("not escaped: %s", doc)
	}
	if !strings.Contains(doc, `<a href="https://example.com/x?y=1&amp;z=2">`) || strings.Contains(doc, `z=2.">`) {
		t.Errorf("link: %s", doc)
	}
	if p.HasRemote {
		t.Error("text has no remote content")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"1", "2", "3"} {
		os.MkdirAll(filepath.Join(dir, n), 0o700)
	}
	Prune(dir, map[string]bool{"2": true})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "2" {
		t.Fatalf("%v", entries)
	}
}
