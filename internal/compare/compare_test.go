package compare

import (
	"fmt"
	"strings"
	"testing"
)

func jsonSide(s string) Side { return Side{Body: []byte(s), ContentType: "application/json"} }

func TestJSONStructuralDiff(t *testing.T) {
	left := `{"event":"push","repo":{"name":"a","stars":1},"tags":["x","y"],"removed":true,"n":1.0}`
	right := `{"event":"push","repo":{"name":"b","stars":1,"private":false},"tags":["x"],"n":1}`
	res := Bodies(jsonSide(left), jsonSide(right))
	if res.Mode != ModeJSON || res.Equal {
		t.Fatalf("mode=%s equal=%v", res.Mode, res.Equal)
	}
	got := map[string]Difference{}
	for _, d := range res.Differences {
		got[d.Path] = d
	}
	want := map[string]string{
		"$.repo.name":    Changed,
		"$.repo.private": Added,
		"$.tags[1]":      Removed,
		"$.removed":      Removed,
	}
	if len(got) != len(want) {
		t.Fatalf("differences = %+v", res.Differences)
	}
	for path, kind := range want {
		if got[path].Kind != kind {
			t.Errorf("%s: kind %q, want %q", path, got[path].Kind, kind)
		}
	}
	if got["$.repo.name"].Left != `"a"` || got["$.repo.name"].Right != `"b"` {
		t.Errorf("values: %+v", got["$.repo.name"])
	}
}

func TestJSONEqualityIgnoresFormattingAndKeyOrder(t *testing.T) {
	res := Bodies(jsonSide(`{"a":1,"b":[1,2]}`), jsonSide("{\n  \"b\": [1, 2],\n  \"a\": 1e0\n}"))
	if !res.Equal || len(res.Differences) != 0 {
		t.Fatalf("expected equal, got %+v", res)
	}
}

func TestTypeChangesAreReported(t *testing.T) {
	res := Bodies(jsonSide(`{"a":"1","b":null,"c":{}}`), jsonSide(`{"a":1,"b":false,"c":[]}`))
	if len(res.Differences) != 3 {
		t.Fatalf("differences = %+v", res.Differences)
	}
}

func TestInvalidJSONFallsBackToTextAndReportsError(t *testing.T) {
	res := Bodies(jsonSide("{\"a\":1}"), jsonSide("{\"a\":\n  1,,}"))
	if res.Mode != ModeText {
		t.Fatalf("mode = %s", res.Mode)
	}
	if res.RightError == "" || !strings.Contains(res.RightError, "line 2") || res.LeftError != "" {
		t.Fatalf("errors: left=%q right=%q", res.LeftError, res.RightError)
	}
	if res.Equal {
		t.Fatal("a parse failure must never be reported as equal")
	}
}

func TestTrailingDataIsInvalidJSON(t *testing.T) {
	res := Bodies(jsonSide(`{"a":1}`), jsonSide(`{"a":1} {"b":2}`))
	if res.Mode != ModeText || res.RightError == "" {
		t.Fatalf("mode=%s right_error=%q", res.Mode, res.RightError)
	}
}

func TestTextDiff(t *testing.T) {
	left := "one\ntwo\nthree\nfour\n"
	right := "one\n2\nthree\nfour\nfive\n"
	res := Bodies(Side{Body: []byte(left)}, Side{Body: []byte(right)})
	if res.Mode != ModeText || res.Equal {
		t.Fatalf("mode=%s equal=%v", res.Mode, res.Equal)
	}
	var ops []string
	for _, l := range res.Lines {
		ops = append(ops, l.Op[:1]+":"+l.Text)
	}
	want := "e:one d:two i:2 e:three e:four i:five"
	if strings.Join(ops, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(ops, " "), want)
	}
}

func TestTextDiffMatchesInputs(t *testing.T) {
	// Reconstruct both sides from the script for a range of edits.
	for n := 0; n < 30; n++ {
		var a, b []string
		for i := 0; i < 40; i++ {
			a = append(a, fmt.Sprintf("line %d", i))
			if (i*7+n)%5 != 0 {
				b = append(b, fmt.Sprintf("line %d", i))
			}
			if (i+n)%9 == 0 {
				b = append(b, fmt.Sprintf("new %d", i))
			}
		}
		lines, _, _ := diffLines(strings.Join(a, "\n"), strings.Join(b, "\n"))
		var ra, rb []string
		for _, l := range lines {
			if l.Op != "insert" {
				ra = append(ra, l.Text)
			}
			if l.Op != "delete" {
				rb = append(rb, l.Text)
			}
		}
		if strings.Join(ra, "\n") != strings.Join(a, "\n") || strings.Join(rb, "\n") != strings.Join(b, "\n") {
			t.Fatalf("case %d: script does not reproduce the inputs", n)
		}
	}
}

func TestLargeTextDiffIsApproximateButCorrect(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&a, "a%d\n", i)
		fmt.Fprintf(&b, "b%d\n", i)
	}
	res := Bodies(Side{Body: []byte(a.String())}, Side{Body: []byte(b.String())})
	if !res.Approximate || len(res.Lines) != 6000 {
		t.Fatalf("approximate=%v lines=%d", res.Approximate, len(res.Lines))
	}
}

func TestBinaryBodies(t *testing.T) {
	res := Bodies(Side{Body: []byte{0, 1, 2}}, Side{Body: []byte{0, 1, 2}})
	if res.Mode != ModeBinary || !res.Equal {
		t.Fatalf("%+v", res)
	}
	res = Bodies(Side{Body: []byte{0, 1, 2}}, Side{Body: []byte("text")})
	if res.Mode != ModeBinary || res.Equal {
		t.Fatalf("%+v", res)
	}
}

func TestEmptyAndTruncatedBodies(t *testing.T) {
	if res := Bodies(Side{}, Side{}); res.Mode != ModeEmpty || !res.Equal {
		t.Fatalf("%+v", res)
	}
	res := Bodies(Side{Body: []byte("a"), Truncated: true}, Side{Body: []byte("a")})
	if !res.IncompleteData {
		t.Fatal("truncated input should be flagged")
	}
}

func TestDifferenceLimit(t *testing.T) {
	var l, r strings.Builder
	l.WriteString("{")
	r.WriteString("{")
	for i := 0; i < MaxDifferences+50; i++ {
		if i > 0 {
			l.WriteString(",")
			r.WriteString(",")
		}
		fmt.Fprintf(&l, `"k%d":1`, i)
		fmt.Fprintf(&r, `"k%d":2`, i)
	}
	l.WriteString("}")
	r.WriteString("}")
	res := Bodies(jsonSide(l.String()), jsonSide(r.String()))
	if !res.Truncated || len(res.Differences) != MaxDifferences || res.Equal {
		t.Fatalf("truncated=%v count=%d", res.Truncated, len(res.Differences))
	}
}

func TestHeaders(t *testing.T) {
	diffs := Headers(
		map[string][]string{"Content-Type": {"application/json"}, "X-A": {"1"}},
		map[string][]string{"content-type": {"text/plain"}, "X-B": {"2"}},
	)
	if len(diffs) != 3 {
		t.Fatalf("diffs = %+v", diffs)
	}
	if diffs[0].Path != "Content-Type" || diffs[0].Kind != Changed {
		t.Errorf("header names should be matched case-insensitively and shown canonically: %+v", diffs[0])
	}
}
