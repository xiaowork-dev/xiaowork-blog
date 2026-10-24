package comment

import (
	"strings"
	"testing"
)

func TestCleanBody(t *testing.T) {
	cases := []struct {
		in, want, problem string
	}{
		{"  你好  ", "你好", ""},
		{"第一行\r\n第二行\r第三行", "第一行\n第二行\n第三行", ""},
		{"a\n\n\n\n\nb", "a\n\nb", ""},    // at most one blank line
		{"行尾空格   \n下一行", "行尾空格\n下一行", ""}, // trailing spaces per line
		{"tab\there", "tab here", ""},
		{"👨‍👩‍👧 👍🏻 ❤️", "👨‍👩‍👧 👍🏻 ❤️", ""}, // ZWJ and variation selectors survive
		{"a\u202eb\u2066c\u200bd\u0000e\u0007f", "abcdef", ""},
		{"<script>alert(1)</script>", "<script>alert(1)</script>", ""}, // plain text; never rendered as HTML
		{"", "", "REQUIRED"},
		{" \n\t\u200b ", "", "REQUIRED"},
		{strings.Repeat("字", 2000), strings.Repeat("字", 2000), ""},
		{strings.Repeat("字", 2001), "", "TOO_LONG"},
		{"bad \xff utf8", "", "INVALID"},
	}
	for _, c := range cases {
		got, problem := CleanBody(c.in)
		if got != c.want || problem != c.problem {
			t.Errorf("CleanBody(%q) = %q, %q; want %q, %q", c.in, got, problem, c.want, c.problem)
		}
	}
}
