package verify

import (
	"strings"
	"testing"
)

func TestParseNativeVerdict(t *testing.T) {
	pass := `{"output":{"type":"review.result","verdict":"pass","summary":"No issues.","findings":[]}}`
	if v, _, ok := ParseNativeVerdict(pass); !ok || v != "ACCEPT" {
		t.Fatalf("pass envelope: v=%q ok=%v", v, ok)
	}
	fail := `{"output":{"type":"review.result","verdict":"needs_changes","summary":"Bad.",` +
		`"findings":[{"priority":"P1","path":"b.py","startLine":6,"title":"boom"}]}}`
	v, rep, ok := ParseNativeVerdict(fail)
	if !ok || v != "REJECT" || !strings.Contains(rep, "[P1] b.py:6: boom") {
		t.Fatalf("needs_changes envelope: v=%q ok=%v rep=%q", v, ok, rep)
	}
	if _, _, ok := ParseNativeVerdict("VERDICT: ACCEPT"); ok {
		t.Fatal("plain text must not parse as a native envelope")
	}
	odd := `{"output":{"type":"review.result","verdict":"pass","summary":"s",` +
		`"findings":[{"priority":"P3","path":"x.py"}]}}`
	if _, _, ok := ParseNativeVerdict(odd); ok {
		t.Fatal("pass carrying findings must be refused")
	}
}
