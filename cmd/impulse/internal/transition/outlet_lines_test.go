package transition

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestLinesAfter pins where a text's lines are after an edit of it: a line keeps its
// number until a line joins or leaves the text above it, a line gofmt realigned is the
// same line, and a line the edit rewrote is placed by the lines around it.
func TestLinesAfter(t *testing.T) {
	t.Parallel()

	const was = "package app\n\ntype App struct {\n\tconsole: one,\n\tlonger:  two,\n}\n\nfunc New() {}\n"
	tests := []struct {
		name string
		now  string
		// want is the number of each line of was, first to last, in now.
		want []int
	}{
		{
			name: "the text as it was",
			now:  was,
			want: []int{1, 2, 3, 4, 5, 6, 7, 8},
		},
		{
			name: "lines joined above, between and below",
			now:  "// Package app.\npackage app\n\ntype App struct {\n\tconsole: one,\n\tportal:  three,\n\tlonger:  two,\n}\n\nfunc New() {}\n\nfunc Late() {}\n",
			want: []int{2, 3, 4, 5, 7, 8, 9, 10},
		},
		{
			name: "a longer key joined, so gofmt realigned the others",
			now:  "package app\n\ntype App struct {\n\tconsole:        one,\n\tlonger:         two,\n\tevenLongerName: three,\n}\n\nfunc New() {}\n",
			want: []int{1, 2, 3, 4, 5, 7, 8, 9},
		},
		{
			name: "a line left the text",
			now:  "package app\n\ntype App struct {\n\tlonger:  two,\n}\n\nfunc New() {}\n",
			want: []int{1, 2, 3, 4, 4, 5, 6, 7},
		},
		{
			name: "a line rewritten where it stood, under two that joined",
			now:  "package app\n\nimport \"net/http\"\n\ntype App struct {\n\tconsole: other,\n\tlonger:  two,\n}\n\nfunc New() {}\n",
			want: []int{1, 2, 5, 6, 7, 8, 9, 10},
		},
		{
			name: "nothing of the text is left",
			now:  "one\ntwo\n",
			want: []int{1, 2, 2, 2, 2, 2, 2, 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			after := linesAfter([]byte(was), []byte(tt.now))
			got := make([]int, 0, len(tt.want))
			for line := range tt.want {
				got = append(got, after(line+1))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("linesAfter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPairLines holds the pairing to what a diff is: over every pair of short texts of
// two words, the pairs join equal lines, keep the order of both texts, and are as many as
// the longest run of lines the two share, which the test counts the slow, plain way.
func TestPairLines(t *testing.T) {
	t.Parallel()

	var texts [][]string
	for length := range 6 {
		for bits := range 1 << length {
			text := make([]string, length)
			for i := range text {
				text[i] = string(rune('a' + bits>>i&1))
			}
			texts = append(texts, text)
		}
	}
	for _, a := range texts {
		for _, b := range texts {
			pairs := pairLines(a, b)
			paired, last := 0, -1
			for i, j := range pairs {
				if j < 0 {
					continue
				}
				if j <= last || j >= len(b) || a[i] != b[j] {
					t.Fatalf("pairLines(%v, %v) = %v: line %d is paired out of order or with another line", a, b, pairs, i)
				}
				paired, last = paired+1, j
			}
			if want := sharedLines(a, b); paired != want {
				t.Fatalf("pairLines(%v, %v) = %v: %d pairs, want %d", a, b, pairs, paired, want)
			}
		}
	}
}

// sharedLines counts the longest run of lines two texts share in order, by the table
// that fills one cell for every line of one text against every line of the other.
func sharedLines(a, b []string) int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := range a {
		for j := range b {
			if a[i] == b[j] {
				table[i+1][j+1] = table[i][j] + 1
			} else {
				table[i+1][j+1] = max(table[i][j+1], table[i+1][j])
			}
		}
	}

	return table[len(a)][len(b)]
}
