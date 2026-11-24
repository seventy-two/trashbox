// Package view writes a message as a page for the window's WebView2 reading
// pane, which serves the view folder at https://trashbox.view/.
//
// Safety is layered: the window turns scripts off and blocks every request
// that isn't to trashbox.view (unless remote images are allowed), and each
// page carries a Content-Security-Policy saying the same, so a mistake in one
// layer is caught by the other.
package view

import (
	"fmt"
	"html"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/seventy-two/trashbox/engine/internal/mailparse"
)

// Host is the virtual host the window maps the view folder to.
const Host = "trashbox.view"

// Page is a written page.
type Page struct {
	Path      string `json:"path"` // file on disk
	URL       string `json:"url"`  // what the reading pane loads
	HasRemote bool   `json:"hasRemote"`
	Remote    bool   `json:"remote"` // remote content allowed on this page
}

var (
	remoteRef   = regexp.MustCompile(`(?i)(?:\b(?:src|background|srcset|poster)\s*=\s*["']?\s*|url\(\s*["']?\s*)(?:https?:)?//`)
	cidRef      = regexp.MustCompile(`(?i)\bcid:([^"'\s)>]+)`)
	metaCharset = regexp.MustCompile(`(?i)<meta\b[^>]*(?:charset|http-equiv)[^>]*>`)
	baseTag     = regexp.MustCompile(`(?i)<base\b[^>]*>`)
	urlInText   = regexp.MustCompile(`\bhttps?://[^\s<>"]+[^\s<>".,;:!?)\]']`)
)

func csp(remote bool) string {
	img, style, font := "'self' data:", "'self' 'unsafe-inline'", "'self' data:"
	if remote {
		img += " https: http:"
		style += " https:"
		font += " https:"
	}
	return "default-src 'none'; img-src " + img + "; style-src " + style + "; font-src " + font +
		"; base-uri 'none'; form-action 'none'"
}

// Write renders message m (id is its cache row) into dir/<id>/index.html.
func Write(dir string, id int64, m *mailparse.Message, remote bool) (Page, error) {
	pageDir := filepath.Join(dir, fmt.Sprint(id))
	os.RemoveAll(pageDir)
	if err := os.MkdirAll(pageDir, 0o700); err != nil {
		return Page{}, err
	}

	var doc string
	hasRemote := false
	if strings.TrimSpace(m.HTML) != "" {
		body := m.HTML
		hasRemote = remoteRef.MatchString(body)
		files := map[string]string{}
		n := 0
		for cid, res := range m.Inline {
			ext := ".bin"
			if exts, _ := mime.ExtensionsByType(res.Type); len(exts) > 0 {
				ext = exts[len(exts)-1]
			}
			n++
			name := fmt.Sprintf("cid-%d%s", n, ext)
			if err := os.WriteFile(filepath.Join(pageDir, name), res.Data, 0o600); err != nil {
				return Page{}, err
			}
			files[strings.ToLower(cid)] = name
		}
		body = cidRef.ReplaceAllStringFunc(body, func(ref string) string {
			cid := strings.ToLower(ref[4:])
			if f, ok := files[cid]; ok {
				return f
			}
			return ref
		})
		body = metaCharset.ReplaceAllString(body, "")
		body = baseTag.ReplaceAllString(body, "")
		doc = head(remote, "html{color-scheme:light;background:#fff}") + body
	} else {
		doc = head(remote, textStyle) + `<pre class="tb-text">` + linkify(m.Text) + "</pre>"
	}

	path := filepath.Join(pageDir, "index.html")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		return Page{}, err
	}
	return Page{
		Path:      path,
		URL:       fmt.Sprintf("https://%s/%d/index.html", Host, id),
		HasRemote: hasRemote,
		Remote:    remote,
	}, nil
}

const textStyle = `:root{color-scheme:light dark}
body{margin:18px 22px;font:15.5px/1.55 "Petrov Sans","Segoe UI",system-ui,sans-serif}
.tb-text{white-space:pre-wrap;word-wrap:break-word;font:inherit;margin:0}
a{color:#2f7de1}`

// head comes before the email's own markup, so its meta tags are the ones in
// force: the browser takes the first charset, and a CSP meta must be in the
// head, which the parser opens implicitly for us.
func head(remote bool, style string) string {
	return `<!doctype html><meta charset="utf-8">` +
		`<meta http-equiv="Content-Security-Policy" content="` + csp(remote) + `">` +
		`<meta name="referrer" content="no-referrer">` +
		`<base target="_blank">` +
		`<style>` + style + `</style>`
}

func linkify(text string) string {
	esc := html.EscapeString(text)
	return urlInText.ReplaceAllStringFunc(esc, func(u string) string {
		return `<a href="` + u + `">` + u + `</a>`
	})
}

// Prune deletes page folders not in keep, so the view folder doesn't grow.
func Prune(dir string, keep map[string]bool) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && !keep[e.Name()] {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
