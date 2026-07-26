// Package markdown contains formatting helpers for text embedded in Mattermost
// Markdown templates.
package markdown

import "strings"

// special lists the characters that must be backslash-escaped so untrusted text
// is rendered literally instead of being interpreted as Markdown syntax.
const special = "\\`*_~[]()<>#|"

// EscapeText makes untrusted, single-line text safe to embed in headings, link
// labels and attribution strings. It deliberately does not process URLs.
func EscapeText(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)
	return escapeSpecial(s)
}

// EscapeBody makes an untrusted multi-line message body safe to embed in a
// Markdown document while keeping its line structure: unlike EscapeText it does
// not flatten line breaks, because a forwarded body must reach Mattermost laid
// out the way its author wrote it.
//
// Line-leading list markers ("- ", "1. ") are intentionally left alone: they
// render close enough to the original and escaping them costs more visible
// backslashes than it saves.
func EscapeBody(s string) string {
	return escapeSpecial(s)
}

// escapeSpecial backslash-escapes every Markdown metacharacter in s.
func escapeSpecial(s string) string {
	if !strings.ContainsAny(s, special) {
		return s
	}
	var escaped strings.Builder
	escaped.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
