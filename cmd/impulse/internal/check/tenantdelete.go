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

// tenantDeleteRule verifies that a tenant that is deleted takes its rows with it. A
// table that carries a tenant's key on the row itself (a tenant-scoped resource whose
// @domain column is its own) references the tenant table on that column with the delete
// rule (ON DELETE CASCADE), or is interleaved in the tenant table with it. A table that
// reaches its tenant through another row (@domain(via: ...)) carries the rule on every
// link of the way: on its own foreign key to that row, on that row's to the next, and so
// on to the tenant table. The generated delete of a tenant removes the tenant's seats
// and its directory; the application's own rows go by the schema, and a link without the
// rule either refuses the delete of a tenant while a row still stands behind it or, with
// no foreign key at all, keeps rows of a tenant that is gone. Nothing compiles against a
// migration, so this check holds it.
//
// The check judges what it can read and says what it did not: the directories record's
// row is removed by the generated delete, so its table needs no rule; and a table no
// migration the check reads creates, or a link of the way it cannot follow, is reported
// as not read, never passed.
type tenantDeleteRule struct{}

// tenantDeleteRuleName is the check's name.
const tenantDeleteRuleName = "tenant-delete-rule"

func (tenantDeleteRule) Name() string { return tenantDeleteRuleName }

func (tenantDeleteRule) Describe() string {
	return "every table that holds a tenant's rows goes with the tenant: a table that carries the tenant's key on the row itself (a bare @domain column) references the tenant table on that column with ON DELETE CASCADE, and a table that reaches its tenant through another row (@domain(via: ...)) carries ON DELETE CASCADE on every link of the way: the foreign key that holds each column of it, or the interleave of its table in the row's"
}

// Meaning explains the invariant for the brief.
func (tenantDeleteRule) Meaning() string {
	return "A tenant going away takes its seats and its rows and nothing else. The generated delete of the tenant record removes the roles held in the tenant, the invitations into it and its directory; the rows of the application's own tables go by the schema. Every table that carries the tenant's key on the row itself (the struct's `// @domain` column, `TenantId` in the skeletons) declares its foreign key to the tenant table with `ON DELETE CASCADE` (`CONSTRAINT FK_<Table>_TenantId FOREIGN KEY (TenantId) REFERENCES Tenants(Id) ON DELETE CASCADE`), as the skeletons' sample table does, so the one request that deletes a tenant deletes its rows in the same transaction. A table that reaches its tenant through another row (`@domain(via: ...)`) carries the rule on every link of that way: its own foreign key to the row it names, that row's foreign key to the next, and the last one's to the tenant table (a table interleaved in the next one with `ON DELETE CASCADE` carries it there). Without the rule on one link the database refuses the delete of a tenant while a row still stands behind that link, and with no foreign key at all the rows of a deleted tenant stay behind. A reference that is on no table's way to its tenant (a row naming a row of a lookup table, or of another table of the same tenant) is the application's to rule either way: this check reads only the way each table reaches its tenant."
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

// named is the foreign key by its constraint's name, or by what it is where the
// migration gave it none.
func (k *foreignKey) named() string {
	if k.name != "" {
		return k.name
	}

	return "its foreign key"
}

// holds reports a foreign key that holds the column, alone or beside others.
func (k *foreignKey) holds(column string) bool {
	return slices.ContainsFunc(k.columns, func(held string) bool {
		return strings.EqualFold(held, column)
	})
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
		way := &tenantWay{a: a, site: site, tables: tables, tenantTable: tenantTable, word: strings.ToLower(site.TenantRecord.Name), columns: map[string]map[string]map[string]string{}}
		for _, r := range siteResources(a, p, site) {
			switch {
			case !r.Table, r.DomainColumn == "" && r.DomainVia == "":
				// A virtual or computed resource, or a method: no table of its own.
				continue
			case site.Directories != nil && site.Directories.Name == r.Name && site.Directories.File == r.File:
				// The generated delete of the tenant removes the directory's row.
				continue
			}
			judged, err := way.judge(&r)
			if err != nil {
				return fail(c.Name(), err.Error())
			}
			switch {
			case judged.finding != "":
				details = append(details, judged.finding)
			case judged.absent != "":
				// A source the check cannot read is named once, for all of its tables.
				if len(notRead) == 0 {
					unread = append(unread, judged.absent)
				}
			case judged.unfollowed != "":
				unread = append(unread, judged.unfollowed)
			case r.DomainVia != "":
				through++
			default:
				held++
			}
		}
	}
	if tenanted == 0 {
		return skip(c.Name(), "no tenant record")
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d table(s) that hold a tenant's rows do not go with the tenant", len(details)), append(details, unread...)...)
	}
	summary := fmt.Sprintf("%d table(s) that carry a tenant's key go with the tenant (ON DELETE CASCADE to %s)", held, strings.Join(records, ", "))
	if through > 0 {
		summary += fmt.Sprintf("; %d table(s) that reach their tenant through another row go with it too (ON DELETE CASCADE on every link of the way)", through)
	}
	if len(unread) > 0 {
		return warn(c.Name(), summary+fmt.Sprintf("; %d could not be read", len(unread)), unread...)
	}

	return pass(c.Name(), summary)
}

// tenantWay judges how the tables of one site's tenant-scoped resources go with their
// tenant: the tables the site's migrations leave, the tenant table and the word for one
// tenant, and the columns of the structs of each resource package a join path was
// followed through, read once.
type tenantWay struct {
	a           *app.App
	site        *app.Site
	tables      map[string]*schemaTable
	tenantTable string
	word        string
	columns     map[string]map[string]map[string]string
}

// judgment is what the check says of one table: a finding (the table does not go with
// its tenant, and why), or a note that it was not read, because no migration read
// creates a table of its way (absent) or a link of the way cannot be followed
// (unfollowed). All empty: the table goes with its tenant.
type judgment struct {
	finding    string
	absent     string
	unfollowed string
}

// judge reads how a resource's table goes with its tenant.
func (w *tenantWay) judge(r *app.DomainResource) (judgment, error) {
	if r.DomainVia != "" {
		return w.through(r)
	}

	return w.keyed(r), nil
}

// keyed judges a table that carries its tenant's key on the row itself: it references
// the tenant table on the key's column with the delete rule, or is interleaved in the
// tenant table with it.
func (w *tenantWay) keyed(r *app.DomainResource) judgment {
	table := resourceOf(w.site, r.Name)
	schema, ok := w.table(table)
	if !ok {
		return judgment{absent: fmt.Sprintf("%s:%d: no migration under %s creates %s, the table of %s, so its delete rule was not read", r.File, r.Line, strings.Join(w.site.Generator.MigrationSources, ", "), table, r.Name)}
	}
	link := linkTo(schema, r.DomainColumn, w.tenantTable)
	if link.state == linkCascades {
		return judgment{}
	}
	keeps := fmt.Sprintf("%s:%d: %s keeps its %s's key in %s, and", r.File, r.Line, r.Name, w.word, r.DomainColumn)
	takes := fmt.Sprintf("so a %s that is deleted takes the table's rows with it", w.word)
	switch link.state {
	case linkInterleaveWithoutRule:
		return judgment{finding: fmt.Sprintf("%s %s interleaves %s in %s with no delete rule, so the delete of a %s that still holds one of the table's rows is refused; say ON DELETE CASCADE on the interleave, %s",
			keeps, schema.file, table, w.tenantTable, w.word, takes)}
	case linkKeyWithoutRule:
		return judgment{finding: fmt.Sprintf("%s %s declares %s from %s(%s) to %s with no delete rule, so the delete of a %s that still holds one of the table's rows is refused; say ON DELETE CASCADE on that foreign key, %s",
			keeps, link.key.file, link.key.named(), table, r.DomainColumn, w.tenantTable, w.word, takes)}
	default:
		return judgment{finding: fmt.Sprintf("%s %s declares no foreign key from %s(%s) to %s, so the rows of a %s that is deleted stay behind; declare one with the delete rule (FOREIGN KEY (%s) REFERENCES %s(<its key>) ON DELETE CASCADE), %s",
			keeps, schema.file, table, r.DomainColumn, w.tenantTable, w.word, r.DomainColumn, w.tenantTable, takes)}
	}
}

// through judges a table that reaches its tenant through another row. It follows the
// way the join path declares, link by link: the table's own column to the row it names,
// then the column of each field the path names on the row reached, the last of them the
// column that holds the tenant's key. Every link must delete its rows with the row it
// reaches. The first link that does not is the finding; a link the migrations read do
// not declare, or a field the resource package does not hold, ends the reading with a
// note of what was not read.
func (w *tenantWay) through(r *app.DomainResource) (judgment, error) {
	own := resourceOf(w.site, r.Name)
	if r.ViaColumn == "" {
		return judgment{unfollowed: fmt.Sprintf("%s:%d: %s reaches its %s through another row (@domain(via: %s)), and the field that carries the annotation has no spanner tag, so the column its way starts on, and the delete rules of the way, were not read", r.File, r.Line, r.Name, w.word, r.DomainVia)}, nil
	}
	reaches := fmt.Sprintf("%s:%d: %s reaches its %s through another row (@domain(via: %s) on %s),", r.File, r.Line, r.Name, w.word, r.DomainVia, r.ViaColumn)
	takes := fmt.Sprintf("so a %s that is deleted takes %s's rows with it", w.word, r.Name)
	table, column := own, r.ViaColumn
	fields := strings.Split(r.DomainVia, ".")
	for i := 0; i <= len(fields); i++ {
		schema, ok := w.table(table)
		if !ok {
			return judgment{absent: fmt.Sprintf("%s no migration under %s creates %s, a table of that way, so the delete rules of the way were not read", reaches, strings.Join(w.site.Generator.MigrationSources, ", "), table)}, nil
		}
		// The last field of the path holds the tenant's key: its link is to the
		// tenant table. Any other link is to the row the column names, whose struct
		// holds the path's next field.
		last, target, next := i == len(fields), w.tenantTable, ""
		if !last {
			var unfollowed string
			var err error
			if target, next, unfollowed, err = w.reached(schema, path.Dir(r.File), table, column, fields[i]); err != nil {
				return judgment{}, err
			}
			if unfollowed != "" {
				return judgment{unfollowed: reaches + " and " + unfollowed}, nil
			}
		}
		link := linkTo(schema, column, target)
		switch link.state {
		case linkCascades:
			// The rows go with the row the link reaches: on to the next link.
		case linkInterleaveWithoutRule:
			return judgment{finding: fmt.Sprintf("%s and %s interleaves %s in %s with no delete rule, so the delete of a %s is refused while a row of %s still stands; say ON DELETE CASCADE on the interleave, as on every link from %s to %s, %s",
				reaches, schema.file, table, target, w.word, table, own, w.tenantTable, takes)}, nil
		case linkKeyWithoutRule:
			return judgment{finding: fmt.Sprintf("%s and %s declares %s from %s(%s) to %s with no delete rule, so the delete of a %s is refused while a row of %s still stands; say ON DELETE CASCADE on that foreign key, as on every link from %s to %s, %s",
				reaches, link.key.file, link.key.named(), table, column, target, w.word, table, own, w.tenantTable, takes)}, nil
		default:
			// Only the last link is asked for a table the column may not reach: any
			// other is to a table one of the column's own declarations names.
			return judgment{finding: fmt.Sprintf("%s and %s declares no foreign key from %s(%s) to %s, so the rows of a %s that is deleted stay behind; where %s is the column that holds the %s's key, declare one with the delete rule (FOREIGN KEY (%s) REFERENCES %s(<its key>) ON DELETE CASCADE), and where it names another row the path stops one field short of that key; %s",
				reaches, schema.file, table, column, w.tenantTable, w.word, column, w.word, column, w.tenantTable, takes)}, nil
		}
		if last || next == "" {
			return judgment{}, nil
		}
		table, column = target, next
	}

	return judgment{}, nil
}

// reached is the table the link on a column of a way reaches, and the column of the
// path's next field on the struct of that table. A column one foreign key holds reaches
// the table the key references; a column no foreign key holds, in a table interleaved in
// a parent, reaches the parent. Where several foreign keys hold the column, the way is
// through the table whose struct holds the path's field, the greatest name first, which
// is how the generator chooses among them. A column that names the tenant's own row ends
// the way there, with no next column. unfollowed says why the link cannot be followed:
// nothing declares it, or no struct of the resource package holds the path's field.
func (w *tenantWay) reached(schema *schemaTable, dir, table, column, field string) (target, next, unfollowed string, err error) {
	targets := targetsOf(schema, column)
	if len(targets) == 0 {
		return "", "", fmt.Sprintf("%s declares no foreign key on %s(%s) and interleaves %s in no table, so the row it names could not be followed and the delete rules of the way were not read", schema.file, table, column, table), nil
	}
	for _, candidate := range targets {
		if strings.EqualFold(candidate, w.tenantTable) {
			return candidate, "", "", nil
		}
		held, err := w.column(dir, candidate, field)
		if err != nil {
			return "", "", "", err
		}
		if held != "" {
			return candidate, held, "", nil
		}
	}
	named := strings.Join(targets, " or ")

	return "", "", fmt.Sprintf("no struct of %s whose table is %s has a field %s with a spanner tag (a struct declared in another directory is not read), so the way could not be followed past %s and its delete rules were not read", dir, named, field, named), nil
}

// table is the table of the name as the migrations left it, whatever the case a foreign
// key wrote the name in.
func (w *tenantWay) table(name string) (*schemaTable, bool) {
	_, schema, ok := tableNamed(w.tables, name)

	return schema, ok
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

// column is the column of a Go field of the struct whose table is the one named, among
// the structs of the resource package in dir, or empty where the package holds no such
// struct or field.
func (w *tenantWay) column(dir, table, field string) (string, error) {
	structs, read := w.columns[dir]
	if !read {
		var err error
		if structs, err = w.a.StructColumns(dir); err != nil {
			return "", err
		}
		w.columns[dir] = structs
	}
	for name, fields := range structs {
		if strings.EqualFold(resourceOf(w.site, name), table) {
			return fields[field], nil
		}
	}

	return "", nil
}

// linkState is what the schema says of one link of a table's way to its tenant.
type linkState int

// The states of a link: its rows go with the row it reaches; a foreign key declares it
// with no delete rule; an interleave declares it with no delete rule; nothing declares
// it.
const (
	linkCascades linkState = iota
	linkKeyWithoutRule
	linkInterleaveWithoutRule
	linkUndeclared
)

// schemaLink is one link as the schema declares it: its state, and the foreign key
// that declares it without the rule, where one does.
type schemaLink struct {
	state linkState
	key   *foreignKey
}

// targetsOf lists the tables the link on a column may reach: the tables the foreign
// keys that hold the column reference, the greatest name first, or, where no foreign key
// holds it, the parent the table is interleaved in.
func targetsOf(schema *schemaTable, column string) []string {
	var targets []string
	for i := range schema.keys {
		key := &schema.keys[i]
		if !key.holds(column) || slices.ContainsFunc(targets, func(target string) bool { return strings.EqualFold(target, key.references) }) {
			continue
		}
		targets = append(targets, key.references)
	}
	if len(targets) == 0 && schema.parent != "" {
		return []string{schema.parent}
	}
	slices.SortFunc(targets, func(a, b string) int {
		return strings.Compare(strings.ToLower(b), strings.ToLower(a))
	})

	return targets
}

// linkTo reads the link a table declares from one of its columns to another table: the
// interleave of the table in that one, or a foreign key that holds the column and
// references it. A table interleaved in the table the link reaches goes by the
// interleave's rule, whatever a foreign key beside it says: the interleave with no rule
// refuses the delete of a row that still has rows under it. Any other link goes by its
// foreign key, and the rows go with the row it reaches where one that holds the column
// says ON DELETE CASCADE.
func linkTo(schema *schemaTable, column, to string) schemaLink {
	if strings.EqualFold(schema.parent, to) {
		if schema.parentCascade {
			return schemaLink{state: linkCascades}
		}

		return schemaLink{state: linkInterleaveWithoutRule}
	}
	var declared *foreignKey
	for i := range schema.keys {
		key := &schema.keys[i]
		if !key.holds(column) || !strings.EqualFold(key.references, to) {
			continue
		}
		if key.cascade {
			return schemaLink{state: linkCascades}
		}
		declared = key
	}
	if declared != nil {
		return schemaLink{state: linkKeyWithoutRule, key: declared}
	}

	return schemaLink{state: linkUndeclared}
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
