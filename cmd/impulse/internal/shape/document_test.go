package shape

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// documentFile is the framework's document of the sign-in shapes, from this package's
// directory.
var documentFile = filepath.Join("..", "..", "..", "..", "docs", "sign-in-shapes.md")

// Where the document names a shape.
var (
	// glanceRowRE matches a row of the document's table of the shapes: the number, the
	// name as a link to the shape's heading, and the name --shape takes.
	glanceRowRE = regexp.MustCompile("(?m)^\\| (\\d+) \\| \\[([^\\]]+)\\]\\(#([^)]+)\\) \\| `([^`]+)` \\|")
	// shapeHeadingRE matches the heading of a shape's own section: its number and name.
	shapeHeadingRE = regexp.MustCompile(`(?m)^### (\d+)\. (.+)$`)
	// shapeFlagRE matches a shape given to --shape anywhere in the document.
	shapeFlagRE = regexp.MustCompile(`--shape[ =]([a-z0-9][a-z0-9-]*)`)
	// notAnchorRE matches what GitHub drops from a heading to make its anchor.
	notAnchorRE = regexp.MustCompile(`[^a-z0-9 -]`)
)

// anchor is the anchor GitHub gives a heading: its text in lower case, without
// punctuation, its spaces as hyphens.
func anchor(heading string) string {
	return strings.ReplaceAll(notAnchorRE.ReplaceAllString(strings.ToLower(heading), ""), " ", "-")
}

// TestDocumentNamesTheShapes holds docs/sign-in-shapes.md to the shapes the command
// knows: its table lists each of them once, in order, by number, name and the name
// --shape takes, each linked to a section whose heading is the shape's number and name,
// and neither the table, the headings nor a --shape in its text names a shape the
// command does not know. A shape added, renamed or removed here fails until the
// document says the same.
func TestDocumentNamesTheShapes(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(documentFile)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	document := string(text)

	shapes := All()
	wantRows, wantHeadings := make([]string, 0, len(shapes)), make([]string, 0, len(shapes))
	for _, s := range shapes {
		heading := fmt.Sprintf("%d. %s", s.Number, s.Name)
		wantRows = append(wantRows, fmt.Sprintf("%d | %s | #%s | %s", s.Number, s.Name, anchor(heading), s.Key))
		wantHeadings = append(wantHeadings, heading)
	}

	rows := glanceRowRE.FindAllStringSubmatch(document, -1)
	gotRows := make([]string, 0, len(rows))
	for _, row := range rows {
		gotRows = append(gotRows, fmt.Sprintf("%s | %s | #%s | %s", row[1], row[2], row[3], row[4]))
	}
	if diff := cmp.Diff(wantRows, gotRows); diff != "" {
		t.Errorf("%s: the table of the shapes is not the shapes the command knows, each as its number | name | link to its heading | name --shape takes (-want +got):\n%s", documentFile, diff)
	}

	headings := shapeHeadingRE.FindAllStringSubmatch(document, -1)
	gotHeadings := make([]string, 0, len(headings))
	for _, heading := range headings {
		gotHeadings = append(gotHeadings, heading[1]+". "+heading[2])
	}
	if diff := cmp.Diff(wantHeadings, gotHeadings); diff != "" {
		t.Errorf("%s: the headings of the shapes' sections are not the shapes the command knows, each as its number and name (-want +got):\n%s", documentFile, diff)
	}

	for _, flag := range shapeFlagRE.FindAllStringSubmatch(document, -1) {
		if _, err := Find(flag[1], shapes, "impulse"); err != nil {
			t.Errorf("%s: %q names a shape the command does not know: %v", documentFile, flag[0], err)
		}
	}
}
