// Package htmltext turns email HTML into readable plain text, for list
// previews, search and quoting in replies.
package htmltext

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var blocks = map[string]bool{
	"p": true, "div": true, "tr": true, "table": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "blockquote": true,
	"section": true, "article": true, "header": true, "footer": true, "hr": true, "pre": true,
}

var skip = map[string]bool{"script": true, "style": true, "head": true, "title": true, "noscript": true}

// Convert returns the text of an HTML document: one blank line between
// blocks, "• " before list items, whitespace collapsed outside <pre>.
func Convert(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skipping, pre := 0, 0
	newline := func(n int) {
		s := b.String()
		have := len(s) - len(strings.TrimRight(s, "\n"))
		if b.Len() == 0 {
			return
		}
		for ; have < n; have++ {
			b.WriteByte('\n')
		}
	}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return tidy(b.String())
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			end := tt == html.EndTagToken
			if skip[tag] && tt != html.SelfClosingTagToken {
				if end {
					skipping = max(0, skipping-1)
				} else {
					skipping++
				}
				continue
			}
			if tag == "pre" {
				if end {
					pre = max(0, pre-1)
				} else {
					pre++
				}
			}
			switch {
			case tag == "br":
				b.WriteByte('\n')
			case tag == "li" && !end:
				newline(1)
				b.WriteString("• ")
			case tag == "td" || tag == "th":
				if end {
					b.WriteByte(' ')
				}
			case blocks[tag]:
				newline(2)
			}
		case html.TextToken:
			if skipping > 0 {
				continue
			}
			t := html.UnescapeString(string(z.Text()))
			if pre == 0 {
				t = collapse(t, endsSpace(b.String()))
			}
			b.WriteString(t)
		}
	}
}

func endsSpace(s string) bool {
	return s == "" || unicode.IsSpace(rune(s[len(s)-1]))
}

// collapse turns runs of whitespace into one space, dropping a leading
// space when the text so far already ends in whitespace.
func collapse(s string, afterSpace bool) string {
	var b strings.Builder
	space := afterSpace
	for _, r := range s {
		if unicode.IsSpace(r) || r == ' ' || r == '‌' || r == '͏' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return b.String()
}

// tidy trims each line and keeps at most one blank line in a row.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := true
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, l)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Snippet is a one-line preview of up to n runes.
func Snippet(text string, n int) string {
	s := collapse(text, true)
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return string(r)
}
