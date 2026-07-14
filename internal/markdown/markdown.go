// Package markdown contains formatting helpers for text embedded in Mattermost
// Markdown templates.
package markdown

import "strings"

// EscapeText makes untrusted, single-line text safe to embed in headings, link
// labels and attribution strings. It deliberately does not process URLs or
// complete message bodies.
func EscapeText(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)

	const special = "\\`*_~[]()<>#|"
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
