package telegram

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"

	"github.com/z0rr0/mmproxy/internal/markdown"
)

const (
	// Surrogate halves, used to reject entity boundaries that would cut a
	// non-BMP character in two.
	highSurrogateMin = 0xD800
	highSurrogateMax = 0xDBFF
	lowSurrogateMin  = 0xDC00
	lowSurrogateMax  = 0xDFFF

	// minBlockFence is the shortest fence that opens a code block.
	minBlockFence = 3

	// spaces are the characters moved out of an emphasis span; Markdown does not
	// treat "** bold **" as emphasis.
	spaces = " \t\r\n"
)

// entityNode is one span of the message body: the whole body when entity is
// nil, a single Telegram entity otherwise. Telegram nests entities but never
// partially overlaps them, so the spans form a tree.
type entityNode struct {
	entity   *models.MessageEntity
	children []*entityNode
	start    int // inclusive, in UTF-16 code units
	end      int // exclusive, in UTF-16 code units
}

// renderEntities turns a forwarded message body and its entities into Mattermost
// Markdown. Text outside a supported entity is escaped, so the post renders what
// the author actually saw in Telegram and a forwarded message cannot smuggle in
// its own markup.
func renderEntities(text string, entities []models.MessageEntity) string {
	if text == "" || len(entities) == 0 {
		return markdown.EscapeBody(text)
	}
	// Offset and Length are UTF-16 code units — neither bytes nor runes — so
	// every boundary has to be computed on the encoded form.
	units := utf16.Encode([]rune(text))
	return renderChildren(units, buildTree(units, entities), false)
}

// buildTree keeps the entities we can render, drops the malformed ones and
// nests the rest under a synthetic root covering the whole body.
func buildTree(units []uint16, entities []models.MessageEntity) *entityNode {
	root := &entityNode{end: len(units)}
	spans := make([]*entityNode, 0, len(entities))
	for i := range entities {
		e := &entities[i]
		if !supportedEntity(e.Type) {
			continue
		}
		if start, end := e.Offset, e.Offset+e.Length; validSpan(units, start, end) {
			spans = append(spans, &entityNode{entity: e, start: start, end: end})
		}
	}
	// Outer spans first, so a parent is always placed before the spans it holds.
	slices.SortStableFunc(spans, func(a, b *entityNode) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(b.end, a.end))
	})

	stack := []*entityNode{root}
	for _, span := range spans {
		for len(stack) > 1 && span.start >= stack[len(stack)-1].end {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		switch {
		case span.end > parent.end:
			continue // partial overlap: Telegram does not emit these, drop defensively
		case parent.entity != nil && verbatimEntity(parent.entity.Type):
			continue // code keeps its content literal, so nesting inside it means nothing
		}
		parent.children = append(parent.children, span)
		stack = append(stack, span)
	}
	return root
}

// validSpan rejects entities whose bounds fall outside the text or land in the
// middle of a surrogate pair.
func validSpan(units []uint16, start, end int) bool {
	switch {
	case start < 0 || end <= start || end > len(units):
		return false
	case units[start] >= lowSurrogateMin && units[start] <= lowSurrogateMax:
		return false
	case units[end-1] >= highSurrogateMin && units[end-1] <= highSurrogateMax:
		return false
	default:
		return true
	}
}

// supportedEntity reports whether the entity has a Mattermost Markdown
// equivalent. Everything else — underline and spoiler have no syntax at all,
// mentions and bare URLs are already readable as text — renders as plain text.
func supportedEntity(t models.MessageEntityType) bool {
	// Telegram grows this enum with every Bot API release, so listing every
	// case is impossible by construction; default is the answer for all of them.
	//nolint:exhaustive // open enum from an external API
	switch t {
	case models.MessageEntityTypeBold, models.MessageEntityTypeItalic,
		models.MessageEntityTypeStrikethrough, models.MessageEntityTypeCode,
		models.MessageEntityTypePre, models.MessageEntityTypeTextLink:
		return true
	default:
		return false
	}
}

// verbatimEntity reports whether the entity content is emitted as-is, without
// escaping or rendering anything nested in it.
func verbatimEntity(t models.MessageEntityType) bool {
	return t == models.MessageEntityTypeCode || t == models.MessageEntityTypePre
}

// renderChildren renders the span of n: the gaps between its children escaped,
// the children themselves through renderNode.
func renderChildren(units []uint16, n *entityNode, insideLink bool) string {
	var w mdWriter
	pos := n.start
	for _, child := range n.children {
		w.text(markdown.EscapeBody(decode(units[pos:child.start])))
		if child.entity.Type == models.MessageEntityTypePre {
			w.block(renderNode(units, child, insideLink))
		} else {
			w.text(renderNode(units, child, insideLink))
		}
		pos = child.end
	}
	w.text(markdown.EscapeBody(decode(units[pos:n.end])))
	return w.result()
}

// renderNode maps one entity onto its Markdown form.
func renderNode(units []uint16, n *entityNode, insideLink bool) string {
	// Only the types supportedEntity accepts can reach this switch.
	//nolint:exhaustive // open enum from an external API
	switch n.entity.Type {
	case models.MessageEntityTypeCode:
		return inlineCode(decode(units[n.start:n.end]))
	case models.MessageEntityTypePre:
		return codeBlock(decode(units[n.start:n.end]), n.entity.Language)
	case models.MessageEntityTypeTextLink:
		return textLink(units, n, insideLink)
	case models.MessageEntityTypeBold:
		return wrap(renderChildren(units, n, insideLink), "**")
	case models.MessageEntityTypeItalic:
		return wrap(renderChildren(units, n, insideLink), "*")
	case models.MessageEntityTypeStrikethrough:
		return wrap(renderChildren(units, n, insideLink), "~~")
	default:
		return renderChildren(units, n, insideLink)
	}
}

// wrap surrounds inner with an emphasis delimiter, leaving surrounding spaces
// outside it: Telegram reports entities that include the trailing space, and
// "** bold **" is not emphasis.
func wrap(inner, delim string) string {
	head := len(inner) - len(strings.TrimLeft(inner, spaces))
	tail := len(strings.TrimRight(inner, spaces))
	if head >= tail {
		return inner // nothing but whitespace to emphasise
	}
	return inner[:head] + delim + inner[head:tail] + delim + inner[tail:]
}

// textLink renders a hyperlink whose target exists only in the entity — the
// visible text carries no URL, so dropping the entity loses it for good.
func textLink(units []uint16, n *entityNode, insideLink bool) string {
	label := renderChildren(units, n, true)
	if insideLink || strings.TrimSpace(label) == "" {
		return label // Markdown has no nested links, and an empty label hides the URL
	}
	target, ok := safeURL(n.entity.URL)
	if !ok {
		return label
	}
	return "[" + label + "](" + target + ")"
}

// safeURL validates a link target from a forwarded message and makes it safe to
// place between Markdown link parentheses.
func safeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil || !allowedScheme(strings.ToLower(u.Scheme)) {
		return "", false
	}

	return escapeURL(raw), true
}

// allowedScheme allowlists the schemes worth turning into a clickable link;
// anything else, javascript: included, stays unlinked text.
func allowedScheme(scheme string) bool {
	switch scheme {
	case "http", "https", "tg", "mailto":
		return true
	default:
		return false
	}
}

// escapeURL percent-encodes what would otherwise close the link target early.
// Control characters need no handling here: url.Parse rejects them outright, so
// safeURL has already turned such a target down.
func escapeURL(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case '(':
			b.WriteString("%28")
		case ')':
			b.WriteString("%29")
		case ' ':
			b.WriteString("%20")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// inlineCode fences content with enough backticks to survive the backticks
// inside it, padding when the content itself starts or ends with one.
func inlineCode(content string) string {
	fence := strings.Repeat("`", maxRun(content, '`')+1)
	var pad string
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") {
		pad = " "
	}
	return fence + pad + content + pad + fence
}

// codeBlock renders a fenced block tagged with the language Telegram reported.
func codeBlock(content, language string) string {
	fence := strings.Repeat("`", max(maxRun(content, '`')+1, minBlockFence))
	return fence + sanitizeLanguage(language) + "\n" + strings.Trim(content, "\n") + "\n" + fence
}

// sanitizeLanguage reduces the untrusted language tag to what a fence info
// string may safely carry: a single word, from a conservative character set.
func sanitizeLanguage(s string) string {
	word := strings.TrimSpace(s)
	if i := strings.IndexFunc(word, unicode.IsSpace); i >= 0 {
		word = word[:i]
	}

	var b strings.Builder
	b.Grow(len(word))
	for _, r := range word {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '+', r == '#', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maxRun returns the length of the longest consecutive run of c in s.
func maxRun(s string, c rune) int {
	var longest, current int
	for _, r := range s {
		if r != c {
			current = 0
			continue
		}
		current++
		longest = max(longest, current)
	}
	return longest
}

// decode turns a slice of UTF-16 code units back into a Go string.
func decode(units []uint16) string {
	return string(utf16.Decode(units))
}

// mdWriter assembles rendered Markdown and keeps fenced code blocks on lines of
// their own — a fence in the middle of a line does not open a block.
type mdWriter struct {
	b        strings.Builder
	last     byte
	needLine bool // a block was just closed: the next text must start on a new line
}

// text appends inline content.
func (w *mdWriter) text(s string) {
	if s == "" {
		return
	}
	if w.needLine && !strings.HasPrefix(s, "\n") {
		w.write("\n")
	}
	w.needLine = false
	w.write(s)
}

// block appends content that has to occupy whole lines.
func (w *mdWriter) block(s string) {
	if w.b.Len() > 0 && w.last != '\n' {
		w.write("\n")
	}
	w.write(s)
	w.needLine = true
}

// write appends s, which must not be empty — every caller checks first.
func (w *mdWriter) write(s string) {
	w.b.WriteString(s)
	w.last = s[len(s)-1]
}

func (w *mdWriter) result() string {
	return w.b.String()
}
