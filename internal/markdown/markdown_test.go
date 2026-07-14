package markdown

import "testing"

func TestEscapeText(t *testing.T) {
	in := "Feed [one](url) #tag *bold* _italic_ ~strike~ `code` <tag> | cell\\path\nnext\tline"
	want := "Feed \\[one\\]\\(url\\) \\#tag \\*bold\\* \\_italic\\_ \\~strike\\~ \\`code\\` \\<tag\\> \\| cell\\\\path next line"
	if got := EscapeText(in); got != want {
		t.Fatalf("EscapeText() = %q, want %q", got, want)
	}
}
