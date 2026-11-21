package transition

import (
	"slices"
	"strings"

	"github.com/impulseframework/impulse/app"
)

// The lines add outlet names by number. The command notes a line while it reads the file,
// and goes on to edit: after the console's move the App gains the outlet's accessor in the
// Configurer and its field, the site's configuration a setting, a test configurer a
// method, each above lines the move had noted. A number written down when the file was
// read then named another line by the time the command ended. So the command keeps each
// file as it was when it noted the file's lines, and writes that part of its report after
// its last edit, with every line named where the command leaves it: the author opens the
// file at the number and finds the line.

// placeLines renames the lines noted of the application's files to their numbers in the
// files as they are now. noted holds each file's text as it was when its lines were noted,
// and every list names lines of that text.
func placeLines(a *app.App, noted map[string][]byte, lists ...mentions) error {
	for rel, was := range noted {
		now, _, err := readFile(a, rel)
		if err != nil {
			return err
		}
		after := linesAfter(was, now)
		for _, list := range lists {
			for i, line := range list[rel] {
				list[rel][i] = after(line)
			}
		}
	}

	return nil
}

// linesAfter answers where a text's lines are after an edit of it: given the number of a
// line of the text as it was, the number of that line in the text as it is. The two texts
// are paired line by line as a diff pairs them, a line being the same line whatever blanks
// lead it or part its words, since gofmt realigns a literal's values when a longer key
// joins them. A line the edit rewrote or removed has no pair: it is placed as far below
// the nearest paired line above it as it was, which is its place where it was rewritten
// where it stood.
func linesAfter(was, now []byte) func(line int) int {
	before, after := lineWords(was), lineWords(now)
	pairs := pairLines(before, after)

	return func(line int) int {
		at := line - 1
		if at < 0 || at >= len(pairs) {
			return line
		}
		for above := at; above >= 0; above-- {
			if pairs[above] >= 0 {
				return max(1, min(pairs[above]+at-above+1, len(after)))
			}
		}

		return max(1, min(line, len(after)))
	}
}

// lineWords is a text's lines, each as its words parted by one blank.
func lineWords(text []byte) []string {
	var lines []string
	for line := range strings.Lines(string(text)) {
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}

	return lines
}

// pairLines pairs the lines two texts share, as a diff does: the most pairs of equal
// lines that keep the order of both, found by the shortest list of edits that makes one
// text of the other (Myers's search, which costs little where the texts differ little).
// It answers, for each line of the first text, the index of its pair in the second, or -1
// where it has none.
func pairLines(a, b []string) []int {
	pairs := make([]int, len(a))
	for i := range pairs {
		pairs[i] = -1
	}
	// A diagonal k holds the places where line x of a faces line x-k of b. reach[mid+k] is
	// how many lines of a the search has passed on diagonal k after the rounds made so
	// far, each round allowing one more line that is in one text alone; rounds keeps
	// reach as it was before each round, which is what the way back reads.
	mid := len(a) + len(b) + 1
	reach := make([]int, 2*mid+1)
	var rounds [][]int
	// fromAbove reports whether a round comes to diagonal k from the diagonal above it,
	// by a line b alone has, and not from the one below, by a line a alone has.
	fromAbove := func(reach []int, round, k int) bool {
		return k == -round || (k != round && reach[mid+k-1] < reach[mid+k+1])
	}

search:
	for round := 0; round <= len(a)+len(b); round++ {
		rounds = append(rounds, slices.Clone(reach))
		for k := -round; k <= round; k += 2 {
			x := reach[mid+k-1] + 1
			if fromAbove(reach, round, k) {
				x = reach[mid+k+1]
			}
			for x < len(a) && x-k < len(b) && a[x] == b[x-k] {
				x++
			}
			reach[mid+k] = x
			if x >= len(a) && x-k >= len(b) {
				break search
			}
		}
	}

	x, y := len(a), len(b)
	for round := len(rounds) - 1; round >= 0; round-- {
		before, k := rounds[round], x-y
		from := k - 1
		if fromAbove(before, round, k) {
			from = k + 1
		}
		fromX := before[mid+from]
		for x > fromX && y > fromX-from {
			x, y = x-1, y-1
			pairs[x] = y
		}
		x, y = fromX, fromX-from
	}

	return pairs
}
