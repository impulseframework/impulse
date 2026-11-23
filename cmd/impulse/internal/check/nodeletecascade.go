package check

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// noDeleteCascade verifies that the database deletes none of an application's rows. A
// foreign key that references a table of the application's records, or an interleave in
// one, declared ON DELETE CASCADE has the database remove rows when the row they name is
// deleted, past the grants of the person who asked, with no change record, no word to
// open pages and no release of the files the rows stored. Rows are deleted in code: a
// record declares what its delete takes (@deleteCascade), and a tenant's delete takes
// every row of the tenant. Nothing compiles against a migration, so this check holds it.
//
// The check reads the up migrations of each site's file:// sources as they leave the
// schema, and a table is one of the application's records when a struct of the site's
// resource package declares it. A rule on a link to any other table (an auth's sessions
// under its users, role grants under their roles) is counted, not judged, and a source
// it cannot read is named, never passed.
type noDeleteCascade struct{}

// noDeleteCascadeName is the check's name.
const noDeleteCascadeName = "no-delete-cascade"

func (noDeleteCascade) Name() string { return noDeleteCascadeName }

func (noDeleteCascade) Describe() string {
	return "no foreign key and no interleave has the database delete rows with one of the application's records: none says ON DELETE CASCADE where the table it references, or the parent it is interleaved in, is a table a struct of the resource package declares"
}

// Meaning explains the invariant for the brief.
func (noDeleteCascade) Meaning() string {
	return "Rows are deleted in code only, never by the database. A foreign key or an interleave declared `ON DELETE CASCADE` has the database remove rows when the row they name is deleted. A row removed that way is not checked against the grants of the person who asked, leaves no change record, is not announced to open pages and keeps the files it stored, and it still counts toward the commit's mutation limit. So no foreign key that references a table of the application's records, and no interleave in one, says `ON DELETE CASCADE`. A delete that should take rows with it is declared on the record (`// @deleteCascade(Kind, ..., refusedBy: Kind, ...)`), and the library deletes those rows in code, each as its own delete would. A tenant's delete takes every row of the tenant the same way with no declaration. A record with rows under it and no declaration is refused in words that say what to delete first. The check reads the up migrations of each site's `file://` sources and compares each rule's target with the tables the structs of the site's resource package declare. A rule on a link to any other table (an auth's sessions under its users, role grants under their roles) is counted in the result and not judged."
}

// deletedInCode is what every finding says after naming the rule: why the rule goes, and
// what takes its place.
const deletedInCode = "Rows are deleted in code only: a row the database deletes is not checked against anyone's grants, leaves no change record, is not announced to open pages and keeps its stored files. Take ON DELETE CASCADE off the %s. Where a delete of the record should take these rows, declare it on the record (// @deleteCascade); a tenant's delete takes its rows in code as it is."

// foreignKey is one foreign key of a table as the migrations declare it: its constraint
// name when it has one, the columns it holds, the table it references, whether its
// delete rule is CASCADE, and the migration that declares it.
type foreignKey struct {
	name       string
	columns    []string
	references string
	cascade    bool
	file       string
}

// named is the foreign key by its constraint's name, or by what it is where the
// migration gave it none.
func (k *foreignKey) named() string {
	if k.name != "" {
		return k.name
	}

	return "its foreign key"
}

// schemaTable is one table as the migrations leave it: the migration that creates it,
// its foreign keys, and the parent it is interleaved in with whether the interleave
// deletes its rows with the parent's.
type schemaTable struct {
	file          string
	keys          []foreignKey
	parent        string
	parentCascade bool
}

// The statements the schema read follows, on text with comments dropped and whitespace
// collapsed: a table created (with its body), a table altered (with what the statement
// does to it), and a table dropped; then, inside either, a foreign key at the table's
// level, a foreign key on a column's own line, a constraint dropped, an interleave with
// its delete rule, that rule set afterwards, and what an ALTER TABLE adds.
var (
	createTableStatementRE = regexp.MustCompile("(?is)^CREATE\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?\\s*\\((.*)$")
	alterTableStatementRE  = regexp.MustCompile("(?is)^ALTER\\s+TABLE\\s+(?:IF\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?\\s+(.*)$")
	dropTableStatementRE   = regexp.MustCompile("(?is)^DROP\\s+TABLE\\s+(?:IF\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?")
	tableForeignKeyRE      = regexp.MustCompile("(?is)^(?:CONSTRAINT\\s+`?([A-Za-z_][A-Za-z0-9_]*)`?\\s+)?FOREIGN\\s+KEY\\s*\\(([^)]*)\\)\\s*REFERENCES\\s+`?([A-Za-z_][A-Za-z0-9_]*)`?\\s*(?:\\([^)]*\\))?(.*)$")
	columnForeignKeyRE     = regexp.MustCompile("(?is)^`?([A-Za-z_][A-Za-z0-9_]*)`?\\s.*\\bREFERENCES\\s+`?([A-Za-z_][A-Za-z0-9_]*)`?\\s*(?:\\([^)]*\\))?(.*)$")
	dropConstraintRE       = regexp.MustCompile("(?is)^DROP\\s+CONSTRAINT\\s+(?:IF\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?")
	interleaveRE           = regexp.MustCompile("(?is)\\bINTERLEAVE\\s+IN\\s+(?:PARENT\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?(.*)$")
	deleteCascadeRE        = regexp.MustCompile(`(?i)\bON\s+DELETE\s+CASCADE\b`)
	setDeleteRuleRE        = regexp.MustCompile(`(?i)^SET\s+ON\s+DELETE\s+(CASCADE|NO\s+ACTION)\b`)
	addedRE                = regexp.MustCompile(`(?is)^ADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?(.*)$`)
)

func (c noDeleteCascade) Run(_ context.Context, env *Env) Result {
	a := env.App
	p := a.Profile()
	if len(p.Sites) == 0 {
		return skip(c.Name(), "no site generator")
	}

	var details, unread []string
	left := 0
	for _, group := range sitesBySchema(p.Sites) {
		records, err := siteRecords(a, group)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		tables, notRead, err := siteSchema(a, group[0])
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		unread = append(unread, notRead...)
		findings, unjudged := cascadesTo(tables, records)
		details = append(details, findings...)
		left += unjudged
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d foreign key(s) or interleave(s) have the database delete rows with one of the application's records", len(details)), append(details, unread...)...)
	}
	summary := "no foreign key or interleave has the database delete rows with one of the application's records"
	if left > 0 {
		summary += fmt.Sprintf("; %d ON DELETE CASCADE rule(s) on a link to a table the application declares no record for were not judged", left)
	}
	if len(unread) > 0 {
		return warn(c.Name(), summary+fmt.Sprintf("; %d migration source(s) could not be read", len(unread)), unread...)
	}

	return pass(c.Name(), summary)
}

// sitesBySchema groups the sites by the migration sources they read, in the order of
// their first site, so a schema two sites share is read and judged once.
func sitesBySchema(sites []app.Site) [][]*app.Site {
	var groups [][]*app.Site
	index := map[string]int{}
	for i := range sites {
		site := &sites[i]
		key := strings.Join(site.Generator.MigrationSources, "\x00")
		if at, ok := index[key]; ok {
			groups[at] = append(groups[at], site)

			continue
		}
		index[key] = len(groups)
		groups = append(groups, []*app.Site{site})
	}

	return groups
}

// siteRecords is the tables of the application's records over a group of sites: the
// table of each struct of each site's resource package, lowercased, since the database
// tells no two tables apart by the case of their names.
func siteRecords(a *app.App, sites []*app.Site) (map[string]bool, error) {
	records := map[string]bool{}
	for _, site := range sites {
		structs, err := a.StructColumns(site.Generator.ResourcePackageDir)
		if err != nil {
			return nil, err
		}
		for name := range structs {
			records[strings.ToLower(resourceOf(site, name))] = true
		}
	}

	return records, nil
}

// cascadesTo judges every ON DELETE CASCADE rule of the tables, in table name order and
// in each table its interleave first, then its foreign keys in the order declared. A
// rule whose target is one of the records is a finding; one whose target is not is
// counted as left.
func cascadesTo(tables map[string]*schemaTable, records map[string]bool) (findings []string, left int) {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		table := tables[name]
		if table.parent != "" && table.parentCascade {
			if !records[strings.ToLower(table.parent)] {
				left++
			} else {
				findings = append(findings, fmt.Sprintf("%s: %s is interleaved in %s with ON DELETE CASCADE, so the database deletes the rows of %s with the row of %s they are under. "+deletedInCode,
					table.file, name, table.parent, name, table.parent, "interleave"))
			}
		}
		for i := range table.keys {
			key := &table.keys[i]
			if !key.cascade {
				continue
			}
			if !records[strings.ToLower(key.references)] {
				left++

				continue
			}
			findings = append(findings, fmt.Sprintf("%s: %s declares %s to %s with ON DELETE CASCADE, so the database deletes the rows of %s with the row of %s they name. "+deletedInCode,
				key.file, name, key.named(), key.references, name, key.references, "foreign key"))
		}
	}

	return findings, left
}

// tableNamed is the table of a name among the tables read, and the name it is held
// under: the database tells no two tables apart by the case of their names, so a
// statement that writes a table's name in another case is about the same table.
func tableNamed(tables map[string]*schemaTable, name string) (held string, schema *schemaTable, ok bool) {
	if schema, ok := tables[name]; ok {
		return name, schema, true
	}
	for known, schema := range tables {
		if strings.EqualFold(known, name) {
			return known, schema, true
		}
	}

	return "", nil, false
}

// siteSchema reads the tables the site's file:// migration sources leave, statement by
// statement in the migrations' order. A source the check cannot read (any other scheme)
// is answered as a note, and the site's tables are then not judged for being absent.
func siteSchema(a *app.App, site *app.Site) (tables map[string]*schemaTable, unread []string, err error) {
	tables = map[string]*schemaTable{}
	for _, src := range site.Generator.MigrationSources {
		if !strings.HasPrefix(src, fileScheme) {
			unread = append(unread, fmt.Sprintf("%s: migration source %s is not a file:// path, so the delete rules of its tables were not read", site.Generator.File, src))

			continue
		}
		if err := readSchema(a, strings.TrimPrefix(src, fileScheme), tables); err != nil {
			return nil, nil, err
		}
	}

	return tables, unread, nil
}

// readSchema applies the up migrations under a root-relative directory to the tables, in
// the order of their names.
func readSchema(a *app.App, dir string, tables map[string]*schemaTable) error {
	entries, err := os.ReadDir(a.Abs(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "os.ReadDir()")
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		rel := path.Join(dir, name)
		data, err := os.ReadFile(a.Abs(rel))
		if err != nil {
			return errors.Wrap(err, "os.ReadFile()")
		}
		for stmt := range strings.SplitSeq(stripSQLComments(string(data)), ";") {
			applyStatement(tables, rel, normalizeSQL(stmt))
		}
	}

	return nil
}

// applyStatement applies one migration statement to the tables: a table created with its
// foreign keys and its interleave, a foreign key added or dropped, an interleave's delete
// rule set, and a table dropped. Any other statement changes nothing the check reads.
func applyStatement(tables map[string]*schemaTable, file, stmt string) {
	if m := createTableStatementRE.FindStringSubmatch(stmt); m != nil {
		body, rest := tableBody(m[2])
		table := &schemaTable{file: file}
		for _, element := range splitElements(body) {
			if key, ok := elementForeignKey(element, file); ok {
				table.keys = append(table.keys, key)
			}
		}
		if in := interleaveRE.FindStringSubmatch(rest); in != nil {
			table.parent, table.parentCascade = in[1], deleteCascadeRE.MatchString(in[2])
		}
		if held, _, ok := tableNamed(tables, m[1]); ok {
			delete(tables, held)
		}
		tables[m[1]] = table

		return
	}
	if m := dropTableStatementRE.FindStringSubmatch(stmt); m != nil {
		if held, _, ok := tableNamed(tables, m[1]); ok {
			delete(tables, held)
		}

		return
	}
	m := alterTableStatementRE.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	_, table, ok := tableNamed(tables, m[1])
	if !ok {
		return
	}
	action := strings.TrimSpace(m[2])
	if dropped := dropConstraintRE.FindStringSubmatch(action); dropped != nil {
		table.keys = slices.DeleteFunc(table.keys, func(key foreignKey) bool {
			return strings.EqualFold(key.name, dropped[1])
		})

		return
	}
	if rule := setDeleteRuleRE.FindStringSubmatch(action); rule != nil {
		table.parentCascade = strings.EqualFold(rule[1], "CASCADE")

		return
	}
	if added := addedRE.FindStringSubmatch(action); added != nil {
		if key, ok := elementForeignKey(added[1], file); ok {
			table.keys = append(table.keys, key)
		}
	}
}

// tableBody splits what follows a CREATE TABLE's opening parenthesis into the body, up
// to the parenthesis that closes it, and what the statement says after it (the primary
// key and the interleave on Spanner).
func tableBody(text string) (body, rest string) {
	depth := 1
	for i, c := range text {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[:i], text[i+1:]
			}
		}
	}

	return text, ""
}

// splitElements splits a table's body into its elements, the columns and the
// constraints, at the commas outside every parenthesis.
func splitElements(body string) []string {
	var elements []string
	depth, start := 0, 0
	for i, c := range body {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				elements = append(elements, strings.TrimSpace(body[start:i]))
				start = i + 1
			}
		}
	}

	return append(elements, strings.TrimSpace(body[start:]))
}

// elementForeignKey reads one element of a table's body, or the action of an ALTER
// TABLE, as a foreign key: a constraint at the table's level, or a column that declares
// its own reference.
func elementForeignKey(element, file string) (foreignKey, bool) {
	if m := tableForeignKeyRE.FindStringSubmatch(element); m != nil {
		return foreignKey{name: m[1], columns: columnNames(m[2]), references: m[3], cascade: deleteCascadeRE.MatchString(m[4]), file: file}, true
	}
	upper := strings.ToUpper(element)
	if strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "PRIMARY") || strings.HasPrefix(upper, "CHECK") {
		return foreignKey{}, false
	}
	if m := columnForeignKeyRE.FindStringSubmatch(element); m != nil {
		return foreignKey{columns: []string{m[1]}, references: m[2], cascade: deleteCascadeRE.MatchString(m[3]), file: file}, true
	}

	return foreignKey{}, false
}

// columnNames reads a parenthesized list of column names, without its quoting.
func columnNames(list string) []string {
	var names []string
	for name := range strings.SplitSeq(list, ",") {
		if name = strings.Trim(strings.TrimSpace(name), "`\""); name != "" {
			names = append(names, name)
		}
	}

	return names
}
