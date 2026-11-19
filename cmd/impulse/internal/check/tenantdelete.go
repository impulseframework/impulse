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

// tenantDeleteRule verifies that a tenant that is deleted takes its rows with it: every
// table that carries a tenant's key on the row itself (a tenant-scoped resource whose
// @domain column is its own) references the tenant table on that column with the delete
// rule (ON DELETE CASCADE), or is interleaved in the tenant table with it. The generated
// delete of a tenant removes the tenant's seats and its directory; the application's own
// rows go by the schema, and a table without the rule either refuses the delete of a
// tenant that still holds one of its rows or, with no foreign key at all, keeps rows of a
// tenant that is gone. Nothing compiles against a migration, so this check holds it.
//
// The check judges what it can read and says what it did not: the directories record's
// row is removed by the generated delete, so its table needs no rule; a table that
// reaches its tenant through another row (@domain(via: ...)) goes as its foreign key to
// that row says, which is the application's choice (a parent may refuse its own delete
// while it has children); and a table no migration the check reads creates is reported
// as not read.
type tenantDeleteRule struct{}

// tenantDeleteRuleName is the check's name.
const tenantDeleteRuleName = "tenant-delete-rule"

func (tenantDeleteRule) Name() string { return tenantDeleteRuleName }

func (tenantDeleteRule) Describe() string {
	return "every table that carries a tenant's key on the row itself (a tenant-scoped resource with a bare @domain column) references the tenant table on that column with ON DELETE CASCADE, so a tenant that is deleted takes its rows with it"
}

// Meaning explains the invariant for the brief.
func (tenantDeleteRule) Meaning() string {
	return "A tenant going away takes its seats and its rows and nothing else. The generated delete of the tenant record removes the roles held in the tenant, the invitations into it and its directory; the rows of the application's own tables go by the schema. Every table that carries the tenant's key on the row itself (the struct's `// @domain` column, `TenantId` in the skeletons) declares its foreign key to the tenant table with `ON DELETE CASCADE` (`CONSTRAINT FK_<Table>_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE`), as the skeletons' sample table does, so the one request that deletes a tenant deletes its rows in the same transaction. Without the rule the database refuses the delete of a tenant that still holds one of the table's rows, and with no foreign key at all the rows of a deleted tenant stay behind. A table that reaches its tenant through another row (`@domain(via: ...)`) goes as its foreign key to that row says, which this check does not judge: where that key carries no delete rule, the parent row, and with it the tenant, cannot be deleted while a child remains."
}

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

func (c tenantDeleteRule) Run(_ context.Context, env *Env) Result {
	a := env.App
	p := a.Profile()
	if len(p.Sites) == 0 {
		return skip(c.Name(), "no site generator")
	}

	var details, unread, records []string
	held, through, tenanted := 0, 0, 0
	for i := range p.Sites {
		site := &p.Sites[i]
		if !site.Tenanted() {
			continue
		}
		tenanted++
		tenantTable := resourceOf(site, site.TenantRecord.Name)
		records = append(records, tenantTable)
		tables, notRead, err := siteSchema(a, site)
		if err != nil {
			return fail(c.Name(), err.Error())
		}
		unread = append(unread, notRead...)
		for _, r := range siteResources(a, p, site) {
			switch {
			case !r.Table, r.DomainColumn == "" && r.DomainVia == "":
				// A virtual or computed resource, or a method: no table of its own.
				continue
			case site.Directories != nil && site.Directories.Name == r.Name && site.Directories.File == r.File:
				// The generated delete of the tenant removes the directory's row.
				continue
			case r.DomainVia != "":
				through++

				continue
			}
			table := resourceOf(site, r.Name)
			schema, ok := tables[table]
			if !ok {
				if len(notRead) == 0 {
					unread = append(unread, fmt.Sprintf("%s:%d: no migration under %s creates %s, the table of %s, so its delete rule was not read", r.File, r.Line, strings.Join(site.Generator.MigrationSources, ", "), table, r.Name))
				}

				continue
			}
			if finding := deleteRuleFinding(&r, table, schema, tenantTable, strings.ToLower(site.TenantRecord.Name)); finding != "" {
				details = append(details, finding)

				continue
			}
			held++
		}
	}
	if tenanted == 0 {
		return skip(c.Name(), "no tenant record")
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d table(s) that carry a tenant's key do not go with the tenant", len(details)), append(details, unread...)...)
	}
	summary := fmt.Sprintf("%d table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to %s)", held, strings.Join(records, ", "))
	if through > 0 {
		summary += fmt.Sprintf("; %d reach their tenant through another row and go as that foreign key says, which is not judged", through)
	}
	if len(unread) > 0 {
		return warn(c.Name(), summary+fmt.Sprintf("; %d could not be read", len(unread)), unread...)
	}

	return pass(c.Name(), summary)
}

// deleteRuleFinding says what is wrong with the way a table that carries a tenant's key
// goes with its tenant, or nothing: the table references the tenant table on the key's
// column with the delete rule, or is interleaved in the tenant table with it.
func deleteRuleFinding(r *app.DomainResource, table string, schema *schemaTable, tenantTable, word string) string {
	interleaved := strings.EqualFold(schema.parent, tenantTable)
	if interleaved && schema.parentCascade {
		return ""
	}
	var declared *foreignKey
	for i := range schema.keys {
		key := &schema.keys[i]
		if !strings.EqualFold(key.references, tenantTable) || len(key.columns) != 1 || !strings.EqualFold(key.columns[0], r.DomainColumn) {
			continue
		}
		if key.cascade {
			return ""
		}
		declared = key
	}
	if declared == nil && interleaved {
		return fmt.Sprintf("%s:%d: %s keeps its %s's key in %s, and %s interleaves %s in %s with no delete rule, so the delete of a %s that still holds one of the table's rows is refused; say ON DELETE CASCADE on the interleave, so a %s that is deleted takes the table's rows with it",
			r.File, r.Line, r.Name, word, r.DomainColumn, schema.file, table, tenantTable, word, word)
	}
	if declared == nil {
		return fmt.Sprintf("%s:%d: %s keeps its %s's key in %s, and %s declares no foreign key from %s(%s) to %s, so the rows of a %s that is deleted stay behind; declare one with the delete rule (FOREIGN KEY (%s) REFERENCES %s(<its key>) ON DELETE CASCADE), so a %s that is deleted takes the table's rows with it",
			r.File, r.Line, r.Name, word, r.DomainColumn, schema.file, table, r.DomainColumn, tenantTable, word, r.DomainColumn, tenantTable, word)
	}
	named := "its foreign key"
	if declared.name != "" {
		named = declared.name
	}

	return fmt.Sprintf("%s:%d: %s keeps its %s's key in %s, and %s declares %s from %s(%s) to %s with no delete rule, so the delete of a %s that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, so a %s that is deleted takes the table's rows with it",
		r.File, r.Line, r.Name, word, r.DomainColumn, declared.file, named, table, r.DomainColumn, tenantTable, word, word)
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
		tables[m[1]] = table

		return
	}
	if m := dropTableStatementRE.FindStringSubmatch(stmt); m != nil {
		delete(tables, m[1])

		return
	}
	m := alterTableStatementRE.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	table, ok := tables[m[1]]
	if !ok {
		return
	}
	action := strings.TrimSpace(m[2])
	if dropped := dropConstraintRE.FindStringSubmatch(action); dropped != nil {
		table.keys = slices.DeleteFunc(table.keys, func(key foreignKey) bool {
			return key.name == dropped[1]
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
