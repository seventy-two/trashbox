package htmltext

import "testing"

func TestConvert(t *testing.T) {
	in := `<html><head><title>x</title><style>p{color:red}</style></head><body>
<p>Hello   <b>there</b>,</p><p>Your&nbsp;code is <strong>123456</strong>.</p>
<ul><li>one</li><li>two</li></ul>
<table><tr><td>A</td><td>B</td></tr></table>line<br>break
<script>alert(1)</script><pre>  keep
   this</pre></body></html>`
	want := "Hello there,\n\nYour code is 123456.\n\n• one\n• two\n\nA B\n\nline\nbreak\n\nkeep\nthis"
	if got := Convert(in); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestSnippet(t *testing.T) {
	if got := Snippet("Hello\n\n  world   and more", 12); got != "Hello world…" {
		t.Fatalf("%q", got)
	}
	if got := Snippet("short", 12); got != "short" {
		t.Fatalf("%q", got)
	}
}
