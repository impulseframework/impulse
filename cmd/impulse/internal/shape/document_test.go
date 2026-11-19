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
	for _, offered := range [][]Shape{OfMethods(), OfTenants(), OfNew()} {
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

// marksRowRE matches a row of the document's table of which sign-in methods each shape
// can use: the shape's number and name, then one mark for each of the six columns.
var marksRowRE = regexp.MustCompile(`(?m)^\| (\d) ([^|]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \|$`)

// TestDocumentMarksTheInviteOnlyShapes holds the document's marks for shapes 5 and 7 to
// what the reading does. Those two shapes are an application whose tenants have no
// directories, and the reading names such an application by its invite-only shape
// whichever sign-in methods the auth registers with the application giving the roles,
// so every method can be used in them: each cell of their rows in the table of the
// methods, and each row of the table in their own sections, says so. A rule that made
// the shape depend on a method again would fail here until the document said which
// method.
func TestDocumentMarksTheInviteOnlyShapes(t *testing.T) {
	t.Parallel()

	const canBeUsed = "Can be used"
	document := readDocument(t)
	// The methods of each column, the first cell after the shape's: a password, then a
	// Google or Microsoft method at each of the four widths, then another provider's.
	// The reading asks no width, so each directory column is every kind of such method.
	password := []app.Method{{Kind: app.MethodPassword, Name: "password"}}
	directory := []app.Method{
		{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication},
		{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication},
	}
	provider := []app.Method{{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}}
	columns := [][]app.Method{password, directory, directory, directory, directory, provider}

	rows := map[string][]string{}
	for _, row := range marksRowRE.FindAllStringSubmatch(document, -1) {
		rows[row[1]+" "+row[2]] = row[3:]
	}
	for _, key := range []string{"tenant-owned-users-invite-only", "global-users-invite-only"} {
		s, err := Find(key, All(), "the test")
		if err != nil {
			t.Fatal(err)
		}
		marks, ok := rows[s.String()]
		if !ok {
			t.Errorf("%s: the table of the methods has no row for %s", documentFile, s)

			continue
		}
		for i, methods := range columns {
			for j := range methods {
				reading := &Reading{Tenants: &app.TenantRecord{Name: "Tenant"}}
				auth := &Auth{Name: "staff"}
				if s.OneTenantPerPerson() {
					auth.OneTenant = "pkg/auth/staff/staff.go:1"
				}
				reading.judgeTenants(auth, append(password[:1:1], methods[j]))
				read := auth.Shape != nil && auth.Shape.Key == s.Key
				if got := marks[i] == canBeUsed; got != read {
					t.Errorf("%s: the row of %s marks column %d %q, and the reading names an application whose tenants have no directories and whose auth has the %s method %v", documentFile, s, i+1, marks[i], methods[j].Name, auth.Shape)
				}
			}
		}

		// The shape's own section lists the methods again.
		heading := fmt.Sprintf("### %d. %s\n", s.Number, s.Name)
		start := strings.Index(document, heading)
		if start < 0 {
			t.Fatalf("%s has no section %q", documentFile, strings.TrimSpace(heading))
		}
		section := document[start+len(heading):]
		if end := strings.Index(section, "\n### "); end >= 0 {
			section = section[:end]
		}
		listed := 0
		for line := range strings.SplitSeq(section, "\n") {
			cells := strings.Split(strings.Trim(line, "| "), " | ")
			if !strings.HasPrefix(line, "| ") || len(cells) != 2 || cells[0] == "Sign-in method" || strings.HasPrefix(cells[0], "---") {
				continue
			}
			listed++
			if cells[1] != canBeUsed {
				t.Errorf("%s: the section of %s marks %q %q, want %q: its tenants have no directories, so every method is a way for an invited person to sign in", documentFile, s, cells[0], cells[1], canBeUsed)
			}
		}
		if listed == 0 {
			t.Errorf("%s: the section of %s lists no sign-in method", documentFile, s)
		}
	}
}

// TestDocumentMarksThePassword holds the document's mark for the password method to
// the command: the shapes a new application starts from with no password method
// (NoPassword) are the shapes whose password the document marks "Cannot be used", in the
// Password column of the table of the methods and in the Password row of the shape's
// own section. A shape that gained or lost its password sign-in in the command would
// fail here until the document said the same, and so would a mark changed in one of the
// document's two places alone.
func TestDocumentMarksThePassword(t *testing.T) {
	t.Parallel()

	const cannotBeUsed = "Cannot be used"
	document := readDocument(t)
	rows := map[string][]string{}
	for _, row := range marksRowRE.FindAllStringSubmatch(document, -1) {
		rows[row[1]+" "+row[2]] = row[3:]
	}
	for _, s := range All() {
		marks, ok := rows[s.String()]
		if !ok {
			t.Errorf("%s: the table of the methods has no row for %s", documentFile, s)

			continue
		}
		if got := marks[0] == cannotBeUsed; got != s.NoPassword() {
			t.Errorf("%s: the row of %s marks the password %q, and the command starts the shape with no password method: %v", documentFile, s, marks[0], s.NoPassword())
		}

		// The shape's own section marks the password again.
		heading := fmt.Sprintf("### %d. %s\n", s.Number, s.Name)
		start := strings.Index(document, heading)
		if start < 0 {
			t.Errorf("%s has no section %q", documentFile, strings.TrimSpace(heading))

			continue
		}
		section := document[start+len(heading):]
		if end := strings.Index(section, "\n### "); end >= 0 {
			section = section[:end]
		}
		var own []string
		for line := range strings.SplitSeq(section, "\n") {
			if mark, found := strings.CutPrefix(line, "| Password | "); found {
				own = append(own, strings.TrimSuffix(mark, " |"))
			}
		}
		if len(own) != 1 {
			t.Errorf("%s: the section of %s marks the password %d time(s), want one row that opens | Password |", documentFile, s, len(own))

			continue
		}
		if own[0] != marks[0] {
			t.Errorf("%s: the section of %s marks the password %q, and the table of the methods %q", documentFile, s, own[0], marks[0])
		}
	}
}
