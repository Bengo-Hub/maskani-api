package richtext

import "testing"

func TestSanitizeKeepsEditorTagsAndDropsScripts(t *testing.T) {
	in := `<p>Water <strong>off</strong> on Friday</p><script>alert(1)</script><p onclick="x()"><a href="javascript:alert(1)">bad</a> <a href="https://shaba.example/notice">read</a></p>`
	got := Sanitize(in)
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if contains(got, bad) {
			t.Fatalf("sanitised output still has %q: %s", bad, got)
		}
	}
	for _, good := range []string{"<strong>off</strong>", `href="https://shaba.example/notice"`, `rel="nofollow`} {
		if !contains(got, good) {
			t.Fatalf("sanitised output lost %q: %s", good, got)
		}
	}
}

func TestPlainTextIsUnchanged(t *testing.T) {
	if got := Sanitize("  Gate closes at 10pm  "); got != "Gate closes at 10pm" {
		t.Fatalf("plain text changed: %q", got)
	}
	if got := Sanitize("<p></p>"); got != "" {
		t.Fatalf("empty editor should be empty, got %q", got)
	}
}

func TestPlainTextRendersBlocksListsAndLinks(t *testing.T) {
	in := `<h2>Water</h2><p>Supply is off on <strong>Friday</strong>.</p><ul><li>Fill tanks</li><li>Report leaks</li></ul><ol><li>One</li><li>Two</li></ol><p>See <a href="https://x.example">the notice</a></p>`
	want := "Water\n\nSupply is off on Friday.\n\n- Fill tanks\n- Report leaks\n\n1. One\n2. Two\n\nSee the notice (https://x.example)"
	if got := PlainText(in); got != want {
		t.Fatalf("plain text:\n%q\nwant\n%q", got, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
