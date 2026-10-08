// Package richtext handles the formatted text the UI's rich text editor (Tiptap, shared-ui-lib
// RichTextEditor) sends: descriptions, notes, notice bodies. Input is sanitised to the editor's own
// tags before it is stored, and PlainText gives the readable text for WhatsApp, SMS-like channels,
// search and the public marketplace. Plain text sent by older clients passes through unchanged.
package richtext

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	xhtml "golang.org/x/net/html"
)

// policy allows what the editor produces and nothing else: paragraphs, two heading levels, bold,
// italic, strike, lists, quotes, code, line breaks and links (opened safely).
var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "h2", "h3", "strong", "b", "em", "i", "s", "ul", "ol", "li", "blockquote", "code", "pre")
	p.AllowAttrs("href").OnElements("a")
	p.AllowStandardURLs()
	p.RequireParseableURLs(true)
	p.AllowURLSchemes("https", "http", "mailto", "tel")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

var tagLike = regexp.MustCompile(`<[a-zA-Z/][^>]*>`)

// IsHTML reports whether text looks like editor HTML rather than plain text.
func IsHTML(s string) bool { return tagLike.MatchString(s) }

// Sanitize returns safe HTML for formatted input, or the text unchanged when it is plain.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !IsHTML(s) {
		return s
	}
	out := strings.TrimSpace(policy.Sanitize(s))
	if out == "<p></p>" {
		return ""
	}
	return out
}

// SanitizePtr sanitises an optional field in place.
func SanitizePtr(p *string) {
	if p != nil {
		v := Sanitize(*p)
		*p = v
	}
}

var blockTags = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "blockquote": true, "pre": true, "ul": true, "ol": true, "div": true}

// PlainText renders formatted text as readable plain text: blocks on their own lines, list items
// with a bullet or number, links as "label (url)". Plain input is returned trimmed.
func PlainText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !IsHTML(s) {
		return s
	}
	doc, err := xhtml.Parse(strings.NewReader(s))
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(tagLike.ReplaceAllString(s, " ")))
	}
	var b strings.Builder
	type list struct {
		ordered bool
		n       int
	}
	var lists []list
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "br":
				b.WriteString("\n")
				return
			case "ul", "ol":
				lists = append(lists, list{ordered: n.Data == "ol"})
				defer func() { lists = lists[:len(lists)-1] }()
			case "li":
				if len(lists) > 0 {
					l := &lists[len(lists)-1]
					l.n++
					b.WriteString(strings.Repeat("  ", len(lists)-1))
					if l.ordered {
						b.WriteString(strconv.Itoa(l.n) + ". ")
					} else {
						b.WriteString("- ")
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == xhtml.ElementNode {
			switch {
			case n.Data == "a":
				if href := attr(n, "href"); href != "" && !strings.Contains(b.String(), href) {
					b.WriteString(" (" + href + ")")
				}
			case n.Data == "li":
				b.WriteString("\n")
			case blockTags[n.Data]:
				b.WriteString("\n\n")
			}
		}
	}
	walk(doc)
	// Collapse runs of blank lines and trailing spaces left by nesting.
	lines := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
