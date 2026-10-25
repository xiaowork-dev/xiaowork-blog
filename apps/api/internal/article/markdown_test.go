package article

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMediaRefsUsesTheMarkdownTree(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := "![a](/media/" + a.String() + "/file)\n" +
		"[link](http://127.0.0.1:3000/media/" + b.String() + "/file?variant=thumb)\n" +
		"![dup](/media/" + a.String() + "/file)\n" +
		"![ext](https://evil.example/media/" + c.String() + "/file)\n" + // other host: not ours
		"`/media/" + d.String() + "/file` in code\n\n" +
		"```\n![in code](/media/" + d.String() + "/file)\n```\n" +
		"<img src=\"/media/" + d.String() + "/file\">\n" // raw HTML is never rendered
	got := mediaRefs(src, "127.0.0.1:3000")
	if !slices.Equal(got, []uuid.UUID{a, b}) {
		t.Fatalf("refs = %v, want [%s %s]", got, a, b)
	}
}

func TestPlainTextAndSummary(t *testing.T) {
	src := "# 标题\n\n第一段 **加粗** [链接](https://x.y)。\n\n<script>alert(1)</script>\n\n```go\nfmt.Println(\"hi\")\n```\n\n- 列表项"
	got := plainText(src)
	want := `标题 第一段 加粗 链接。 fmt.Println("hi") 列表项`
	if got != want {
		t.Fatalf("plainText = %q\nwant        %q", got, want)
	}
	long := ""
	for i := 0; i < 50; i++ {
		long += "容器化部署"
	}
	if s := autoSummary(long); len([]rune(s)) != summaryRunes+1 || s[len(s)-len("…"):] != "…" {
		t.Fatalf("summary = %q", s)
	}
}

func TestSummaryTextSkipsNoise(t *testing.T) {
	src := "```bash\nsudo git clone https://github.com/x/y.git /opt/y\n```\n\n" +
		"| 字段 | 说明 |\n|---|---|\n| a | b |\n\n" +
		"![示意图](/media/0b6e1c9e-0000-4000-8000-000000000000/file)\n\n" +
		"参考：https://example.test/a/very/long/path/that/should/not/appear/in/the/summary?q=1\n\n" +
		"(见 https://example.test/another/long/path/inside/parentheses/that/is/not/linkified)\n\n" +
		"<div>raw</div>\n\n" +
		"## 小节\n\n真正的第一段，含 `行内代码` 与[链接文字](https://x.y)。\n\n    indented code\n"
	got := summaryText(src)
	want := "(见) 小节 真正的第一段，含 行内代码 与链接文字。"
	if got != want {
		t.Fatalf("summaryText = %q\nwant          %q", got, want)
	}
	// Search still sees the code (plainText is unchanged).
	if p := plainText(src); !strings.Contains(p, "sudo git clone") {
		t.Fatalf("plainText lost the code: %q", p)
	}
	if s := summaryText("```go\nfmt.Println(1)\n```"); s != "" {
		t.Fatalf("code-only article summary = %q, want empty", s)
	}
}

func TestSlugs(t *testing.T) {
	cases := map[string]string{"Docker Compose Guide!": "docker-compose-guide", "  Go 1.27  新特性 ": "go-1-27", "容器化": "", "--A--B--": "a-b"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ok := range []string{"a", "docker-guide", "go-1-27"} {
		if !validSlug(ok, 160) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "A", "a--b", "-a", "a-", "a b", "a/b", "中文", "a_b"} {
		if validSlug(bad, 160) {
			t.Errorf("%q accepted", bad)
		}
	}
}
