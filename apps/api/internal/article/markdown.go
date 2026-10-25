// Package article implements the article lifecycle of PRD POST-01~11: one editable draft, one public
// snapshot, explicit publish (DD §5, T03/T04; API-DD §4.3, §5.1~5.4).
package article

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

var mediaPath = regexp.MustCompile(`^/media/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/file$`)

// mediaRefs returns the site media referenced by images and links in the Markdown. It walks the parsed
// document (DD §7.2: no plain substring matching), so references inside code blocks or raw HTML are not
// counted — raw HTML is never rendered either (API-DD §5.7). Absolute URLs count only on the site's host.
func mediaRefs(source, siteHost string) []uuid.UUID {
	src := []byte(source)
	doc := md.Parser().Parse(text.NewReader(src))
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(dest string) {
		u, err := url.Parse(strings.TrimSpace(dest))
		if err != nil || (u.Host != "" && !strings.EqualFold(u.Host, siteHost)) || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		m := mediaPath.FindStringSubmatch(u.Path)
		if m == nil {
			return
		}
		id := uuid.MustParse(m[1])
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Image:
			add(string(n.Destination))
		case *ast.Link:
			add(string(n.Destination))
		case *ast.AutoLink:
			add(string(n.URL(src)))
		}
		return ast.WalkContinue, nil
	})
	return out
}

// plainText extracts readable text (including code) for search (DD §9.1: body_plain_text, no Markdown
// syntax). Raw HTML is dropped because it is never rendered.
func plainText(source string) string {
	src := []byte(source)
	doc := md.Parser().Parse(text.NewReader(src))
	var b strings.Builder
	space := func() {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(src))
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		case *ast.AutoLink:
			b.Write(n.Label(src))
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			space()
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				b.Write(seg.Value(src))
			}
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock, *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		default:
			if n.Type() == ast.TypeBlock {
				space()
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// summaryText extracts the prose a reader would see first, for the automatic summary (P8-B8). Unlike
// plainText (search), it leaves out code blocks, tables, images, raw HTML and bare URLs, which read as
// noise in an article card; inline code and link texts stay because they are part of the sentence.
func summaryText(source string) string {
	src := []byte(source)
	doc := md.Parser().Parse(text.NewReader(src))
	var b strings.Builder
	space := func() {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(src))
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock, *ast.RawHTML, *ast.Image, *ast.AutoLink, *east.Table:
			return ast.WalkSkipChildren, nil
		default:
			if n.Type() == ast.TypeBlock {
				space()
			}
		}
		return ast.WalkContinue, nil
	})
	// Long URLs written as plain text (not linkified, e.g. right after a full-width colon) are noise too.
	// A short label glued to the URL ("参考：https://…") goes with it.
	prose := longURL.ReplaceAllStringFunc(b.String(), func(m string) string {
		if len(m)-strings.IndexAny(m, "hw") > 30 {
			return " "
		}
		return m
	})
	prose = strings.Join(strings.Fields(prose), " ")
	return emptyParens.ReplaceAllString(strings.NewReplacer(" )", ")", " ）", "）").Replace(prose), "")
}

var (
	longURL     = regexp.MustCompile(`(?:[^\s:：]{1,8}[:：] ?)?(?:https?://|www\.)[^\s，。；、）)]+`)
	emptyParens = regexp.MustCompile(` ?(?:\(\)|（）)`)
)

const summaryRunes = 160

// autoSummary is the fallback public summary when the draft has none (PRD "摘要可以自动生成"). It is
// given summaryText, not the search text, so commands in code blocks never end up on the home page.
func autoSummary(plain string) string {
	if utf8.RuneCountInString(plain) <= summaryRunes {
		return plain
	}
	r := []rune(plain)
	return strings.TrimSpace(string(r[:summaryRunes])) + "…"
}

// Slugs: lowercase ASCII letters, digits and single hyphens (ADR-0004). Kept ASCII so URLs stay readable
// and unambiguous; Chinese titles fall back to an id-based slug.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validSlug(s string, max int) bool { return len(s) <= max && slugPattern.MatchString(s) }

// slugify derives a slug candidate from a title; "" when the title has no ASCII letters or digits.
func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

// MediaRefs and PlainText are shared with the about page (internal/site), which follows the same
// reference and plain-text rules as articles.
func MediaRefs(source, siteHost string) []uuid.UUID { return mediaRefs(source, siteHost) }

func PlainText(source string) string { return plainText(source) }
