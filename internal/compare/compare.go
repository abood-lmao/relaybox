// Package compare finds differences between two captured payloads.
//
// When both bodies are valid JSON they are compared structurally: object
// keys are matched by name, and arrays are compared position by position
// (element 0 with element 0, and so on), so an insertion near the start of
// an array shows up as changes to every later element. Otherwise the bodies
// are compared line by line as text. Binary bodies are only compared for
// equality.
package compare

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/abood-lmao/relaybox/internal/redact"
)

// Modes.
const (
	ModeJSON   = "json"
	ModeText   = "text"
	ModeBinary = "binary"
	ModeEmpty  = "empty"
)

// Change kinds.
const (
	Added   = "added"
	Removed = "removed"
	Changed = "changed"
)

// Limits keep comparisons fast and responses small.
const (
	MaxDifferences = 1000
	MaxDepth       = 64
	MaxValueBytes  = 2048
	MaxTextLines   = 20000
	maxEditCost    = 1000
)

// Side is one payload to compare.
type Side struct {
	Body        []byte
	ContentType string
	Truncated   bool
}

// Difference is one structural JSON change. Left and Right hold compact
// JSON values, cut to MaxValueBytes.
type Difference struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Left  string `json:"left,omitempty"`
	Right string `json:"right,omitempty"`
}

// Line is one line of a text diff.
type Line struct {
	Op    string `json:"op"` // "equal", "insert", "delete"
	Text  string `json:"text"`
	Left  int    `json:"left,omitempty"`  // 1-based line number in the left body
	Right int    `json:"right,omitempty"` // 1-based line number in the right body
}

// Result describes the comparison.
type Result struct {
	Mode           string       `json:"mode"`
	Equal          bool         `json:"equal"`
	LeftError      string       `json:"left_error,omitempty"`
	RightError     string       `json:"right_error,omitempty"`
	Differences    []Difference `json:"differences"`
	Lines          []Line       `json:"lines"`
	Truncated      bool         `json:"truncated"`       // the list of differences was cut
	Approximate    bool         `json:"approximate"`     // the text diff is not minimal
	IncompleteData bool         `json:"incomplete_data"` // a stored body was truncated at capture
	Note           string       `json:"note,omitempty"`
}

// Bodies compares two payloads.
func Bodies(left, right Side) Result {
	res := Result{Differences: []Difference{}, Lines: []Line{}, IncompleteData: left.Truncated || right.Truncated}

	if len(left.Body) == 0 && len(right.Body) == 0 {
		res.Mode, res.Equal = ModeEmpty, true
		return res
	}
	if isBinary(left.Body) || isBinary(right.Body) {
		res.Mode = ModeBinary
		res.Equal = sha256.Sum256(left.Body) == sha256.Sum256(right.Body)
		res.Note = fmt.Sprintf("At least one body is binary, so only equality is checked (%d and %d bytes).", len(left.Body), len(right.Body))
		return res
	}

	wantJSON := looksJSON(left) || looksJSON(right)
	if wantJSON {
		lv, lerr := parseJSON(left.Body)
		rv, rerr := parseJSON(right.Body)
		if lerr == nil && rerr == nil {
			res.Mode = ModeJSON
			d := differ{}
			d.compare("$", lv, rv, 0)
			res.Differences = d.diffs
			res.Truncated = d.truncated
			res.Equal = len(d.diffs) == 0 && !d.truncated
			return res
		}
		if lerr != nil {
			res.LeftError = describeJSONError(left.Body, lerr)
		}
		if rerr != nil {
			res.RightError = describeJSONError(right.Body, rerr)
		}
		res.Note = "At least one body is not valid JSON, so the bodies are compared as text."
	}

	res.Mode = ModeText
	res.Lines, res.Approximate, res.Truncated = diffLines(string(left.Body), string(right.Body))
	res.Equal = bytes.Equal(left.Body, right.Body)
	return res
}

func looksJSON(s Side) bool {
	ct := strings.ToLower(s.ContentType)
	return strings.Contains(ct, "json") || redact.LooksLikeJSON(s.Body)
}

func isBinary(b []byte) bool {
	return !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0
}

// parseJSON decodes exactly one JSON value, keeping numbers exact.
func parseJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the first JSON value")
	}
	return v, nil
}

// JSONError returns a description of why body is not a single valid JSON
// value, or "" when it is.
func JSONError(body []byte) string {
	if _, err := parseJSON(body); err != nil {
		return describeJSONError(body, err)
	}
	return ""
}

func describeJSONError(body []byte, err error) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return "The body is empty."
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		line, col := position(body, syn.Offset)
		return fmt.Sprintf("Invalid JSON at line %d, column %d: %s", line, col, syn.Error())
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "Invalid JSON: the document ends unexpectedly."
	}
	return "Invalid JSON: " + err.Error()
}

func position(body []byte, offset int64) (line, col int) {
	line, col = 1, 1
	for i := int64(0); i < offset-1 && i < int64(len(body)); i++ {
		if body[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

type differ struct {
	diffs     []Difference
	truncated bool
}

func (d *differ) add(path, kind string, left, right any, hasLeft, hasRight bool) {
	if len(d.diffs) >= MaxDifferences {
		d.truncated = true
		return
	}
	diff := Difference{Path: path, Kind: kind}
	if hasLeft {
		diff.Left = render(left)
	}
	if hasRight {
		diff.Right = render(right)
	}
	d.diffs = append(d.diffs, diff)
}

func (d *differ) compare(path string, l, r any, depth int) {
	if d.truncated {
		return
	}
	if depth > MaxDepth {
		if render(l) != render(r) {
			d.add(path, Changed, l, r, true, true)
		}
		return
	}
	switch lv := l.(type) {
	case map[string]any:
		rv, ok := r.(map[string]any)
		if !ok {
			d.add(path, Changed, l, r, true, true)
			return
		}
		keys := make(map[string]bool, len(lv)+len(rv))
		for k := range lv {
			keys[k] = true
		}
		for k := range rv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			child := redact.JoinPath(path, k)
			a, inL := lv[k]
			b, inR := rv[k]
			switch {
			case inL && !inR:
				d.add(child, Removed, a, nil, true, false)
			case !inL && inR:
				d.add(child, Added, nil, b, false, true)
			default:
				d.compare(child, a, b, depth+1)
			}
		}
	case []any:
		rv, ok := r.([]any)
		if !ok {
			d.add(path, Changed, l, r, true, true)
			return
		}
		for i := 0; i < max(len(lv), len(rv)); i++ {
			child := path + "[" + strconv.Itoa(i) + "]"
			switch {
			case i >= len(rv):
				d.add(child, Removed, lv[i], nil, true, false)
			case i >= len(lv):
				d.add(child, Added, nil, rv[i], false, true)
			default:
				d.compare(child, lv[i], rv[i], depth+1)
			}
		}
	case json.Number:
		rv, ok := r.(json.Number)
		if !ok || !numbersEqual(lv, rv) {
			d.add(path, Changed, l, r, true, true)
		}
	default:
		// strings, booleans, and null
		if fmt.Sprintf("%T", l) != fmt.Sprintf("%T", r) || l != r {
			d.add(path, Changed, l, r, true, true)
		}
	}
}

// numbersEqual compares JSON numbers by value, so 1, 1.0 and 1e0 are equal.
func numbersEqual(a, b json.Number) bool {
	if a == b {
		return true
	}
	x, okA := new(big.Rat).SetString(string(a))
	y, okB := new(big.Rat).SetString(string(b))
	return okA && okB && x.Cmp(y) == 0
}

func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if len(b) > MaxValueBytes {
		cut := MaxValueBytes
		for cut > 0 && !utf8.RuneStart(b[cut]) {
			cut--
		}
		return string(b[:cut]) + "..."
	}
	return string(b)
}

// diffLines computes a line diff using Myers' algorithm after trimming the
// common prefix and suffix. When the edit distance exceeds maxEditCost the
// middle section is reported as fully replaced (approximate=true).
func diffLines(a, b string) (lines []Line, approximate, truncated bool) {
	al := splitLines(a)
	bl := splitLines(b)
	if len(al) > MaxTextLines {
		al, truncated = al[:MaxTextLines], true
	}
	if len(bl) > MaxTextLines {
		bl, truncated = bl[:MaxTextLines], true
	}

	prefix := 0
	for prefix < len(al) && prefix < len(bl) && al[prefix] == bl[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(al)-prefix && suffix < len(bl)-prefix && al[len(al)-1-suffix] == bl[len(bl)-1-suffix] {
		suffix++
	}

	out := make([]Line, 0, max(len(al), len(bl)))
	for i := 0; i < prefix; i++ {
		out = append(out, Line{Op: "equal", Text: al[i], Left: i + 1, Right: i + 1})
	}
	midA := al[prefix : len(al)-suffix]
	midB := bl[prefix : len(bl)-suffix]
	ops, ok := myers(midA, midB)
	if !ok {
		approximate = true
		ops = ops[:0]
		for range midA {
			ops = append(ops, 'd')
		}
		for range midB {
			ops = append(ops, 'i')
		}
	}
	ai, bi := prefix, prefix
	for _, op := range ops {
		switch op {
		case 'e':
			out = append(out, Line{Op: "equal", Text: al[ai], Left: ai + 1, Right: bi + 1})
			ai++
			bi++
		case 'd':
			out = append(out, Line{Op: "delete", Text: al[ai], Left: ai + 1})
			ai++
		case 'i':
			out = append(out, Line{Op: "insert", Text: bl[bi], Right: bi + 1})
			bi++
		}
	}
	for i := 0; i < suffix; i++ {
		out = append(out, Line{Op: "equal", Text: al[ai+i], Left: ai + i + 1, Right: bi + i + 1})
	}
	return out, approximate, truncated
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// myers returns an edit script of 'e' (equal), 'd' (delete from a) and 'i'
// (insert from b). It gives up when more than maxEditCost edits are needed.
// Each step only saves the diagonals it can reach, so memory grows with the
// square of the edit distance rather than the input size.
func myers(a, b []string) ([]byte, bool) {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, true
	}
	limit := min(n+m, maxEditCost)
	offset := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		// trace[d] holds diagonals -d-1..d+1 as they were before step d.
		trace = append(trace, append([]int(nil), v[offset-d-1:offset+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, d), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, a, b []string, dEnd int) []byte {
	x, y := len(a), len(b)
	var ops []byte
	for d := dEnd; d > 0; d-- {
		saved := trace[d]
		v := func(k int) int { return saved[k+d+1] }
		k := x - y
		var prevK int
		if k == -d || (k != d && v(k-1) < v(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, 'e')
			x--
			y--
		}
		if x == prevX {
			ops = append(ops, 'i')
		} else {
			ops = append(ops, 'd')
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		ops = append(ops, 'e')
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Headers compares two header maps by name (case-insensitively).
func Headers(left, right map[string][]string) []Difference {
	norm := func(h map[string][]string) map[string]string {
		out := make(map[string]string, len(h))
		for k, v := range h {
			out[textproto.CanonicalMIMEHeaderKey(k)] = strings.Join(v, ", ")
		}
		return out
	}
	l, r := norm(left), norm(right)
	names := make(map[string]bool)
	for k := range l {
		names[k] = true
	}
	for k := range r {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	diffs := []Difference{}
	for _, k := range sorted {
		lv, inL := l[k]
		rv, inR := r[k]
		switch {
		case inL && !inR:
			diffs = append(diffs, Difference{Path: k, Kind: Removed, Left: lv})
		case !inL && inR:
			diffs = append(diffs, Difference{Path: k, Kind: Added, Right: rv})
		case lv != rv:
			diffs = append(diffs, Difference{Path: k, Kind: Changed, Left: lv, Right: rv})
		}
	}
	return diffs
}
