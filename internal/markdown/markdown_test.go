package markdown

import "testing"

func TestEscapeText(t *testing.T) {
	in := "Feed [one](url) #tag *bold* _italic_ ~strike~ `code` <tag> | cell\\path\nnext\tline"
	want := "Feed \\[one\\]\\(url\\) \\#tag \\*bold\\* \\_italic\\_ \\~strike\\~ \\`code\\` \\<tag\\> \\| cell\\\\path next line"
	if got := EscapeText(in); got != want {
		t.Fatalf("EscapeText() = %q, want %q", got, want)
	}
}

func TestEscapeBody(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "line breaks and tabs survive",
			in:   "first\nsecond\r\nthird\tcolumn",
			want: "first\nsecond\r\nthird\tcolumn",
		},
		{
			name: "markdown specials are escaped",
			in:   "*bold* _it_ [l](u) `c` ~s~ <t> #h | c\\p",
			want: "\\*bold\\* \\_it\\_ \\[l\\]\\(u\\) \\`c\\` \\~s\\~ \\<t\\> \\#h \\| c\\\\p",
		},
		{
			name: "plain text is returned untouched",
			in:   "просто текст без разметки",
			want: "просто текст без разметки",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "specials keep their position across lines",
			in:   "line one\n**line two**",
			want: "line one\n\\*\\*line two\\*\\*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeBody(tt.in); got != tt.want {
				t.Errorf("EscapeBody() = %q, want %q", got, tt.want)
			}
		})
	}
}
