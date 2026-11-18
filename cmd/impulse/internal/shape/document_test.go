package shape

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// documentFile is the framework's document of the sign-in shapes, from this package's
// directory: the path the --shape flag's help names (Document), under the repository's
// root.
var documentFile = filepath.Join("..", "..", "..", "..", filepath.FromSlash(Document))

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
	// startingPointRE matches the document's quote of what a command given --shape
	// prints: the shape it started from, by number and name, and what it says of the
	// method it wrote.
	startingPointRE = regexp.MustCompile(`(?m)^Starting point: (\d+) (.+)\.\n {2}- (.+)$`)
	// sourceLineRE matches a position in a Go source file with its line.
	sourceLineRE = regexp.MustCompile(`\.go:\d+`)
)

// readDocument reads the framework's document of the sign-in shapes.
func readDocument(t *testing.T) string {
	t.Helper()

	text, err := os.ReadFile(documentFile)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	return string(text)
}

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

	document := readDocument(t)
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

// TestUsage_pointsAtTheDocument holds the help of every command's --shape flag to its
// last words: where the shapes are described, by the path of the document from the
// root of the framework's repository, which TestDocumentNamesTheShapes reads there.
func TestUsage_pointsAtTheDocument(t *testing.T) {
	t.Parallel()

	const want = "; the shapes are described in docs/sign-in-shapes.md of the framework's repository"
	for _, offered := range [][]Shape{OfMethods(), OfTenants()} {
		if got := Usage(offered, "the choices"); !strings.HasSuffix(got, want) {
			t.Errorf("Usage() = %q, want it to end %q", got, want)
		}
	}
}

// TestDocumentQuotesTheStartingPoints holds the document's quotes of what a command
// given --shape prints to the command's words: the shape by number and name, and what
// the shape says of the method the example beside the quote adds, with the choices the
// shape fills in for it. The line impulse audit prints for each shape is held to the
// document by the audit's own test, which can print it.
func TestDocumentQuotesTheStartingPoints(t *testing.T) {
	t.Parallel()

	// The method each quoted example adds, by the shape it is given, as the command
	// line names it: its kind and its name.
	added := map[string]authpkg.Method{
		"directory-members": {Kind: app.MethodGoogle, Name: "workspace"},
		"open-sign-up":      {Kind: app.MethodGoogle, Name: "google"},
	}
	quoted := startingPointRE.FindAllStringSubmatch(readDocument(t), -1)
	if len(quoted) != len(added) {
		t.Errorf("%s quotes %d starting points, want the %d held here", documentFile, len(quoted), len(added))
	}
	for _, quote := range quoted {
		s, err := Find(quote[1], All(), "impulse")
		if err != nil {
			t.Errorf("%s: %q names a shape the command does not know: %v", documentFile, quote[0], err)

			continue
		}
		method, ok := added[s.Key]
		if !ok {
			t.Errorf("%s quotes a starting point of %s, which this test holds to no method", documentFile, s)

			continue
		}
		if err := s.Method(&method); err != nil {
			t.Fatalf("Method() error = %v", err)
		}
		if diff := cmp.Diff(s.String(), quote[1]+" "+quote[2]); diff != "" {
			t.Errorf("%s: the starting point is not the shape by number and name (-want +got):\n%s", documentFile, diff)
		}
		if diff := cmp.Diff(s.Says(&method, OpensWithRole), quote[3]); diff != "" {
			t.Errorf("%s: the starting point of %s is not what the command says of the %s method (-want +got):\n%s", documentFile, s, method.Name, diff)
		}
	}
}

// TestDocumentNamesNoSourceLine holds the document to naming a source file without its
// line: a line moves with every edit of its file, so a number quoted from one run of
// impulse audit goes stale without anything failing. Where the command prints a line,
// the document writes N.
func TestDocumentNamesNoSourceLine(t *testing.T) {
	t.Parallel()

	for i, line := range strings.Split(readDocument(t), "\n") {
		if position := sourceLineRE.FindString(line); position != "" {
			t.Errorf("%s:%d names a line of a source file (%s): say the file, and write N where the command prints the line", documentFile, i+1, position)
		}
	}
}
