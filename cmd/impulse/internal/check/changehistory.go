package check

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"

	"github.com/impulseframework/impulse/app"
)

// changeHistory verifies the change history table: the DataChangeEvents table a resource
// that tracks its changes writes to carries every column the resource package of the
// framework module the application pins declares for it (resource.DataChangeEventsDDL,
// read from the module's source as feature-flags reads its statements), created with the
// table or added by a later migration, and the statement that creates the table keys it
// as the library's does, spacing, backquotes and letter case aside. A tracked write fills
// exactly the library's columns, and a record's history is read as one range of the key.
// An index of the application's on the table is its own affair.
type changeHistory struct{}

// changeHistoryName is the check's name.
const changeHistoryName = "change-history"

// The function that declares the change history statements, and the table they create.
const (
	changeHistoryDDLFunc = "DataChangeEventsDDL"
	changeHistoryTable   = "DataChangeEvents"
)

// primaryKeyRE finds the key a CREATE TABLE statement gives its table: what stands
// between the parentheses after the PRIMARY KEY that follows the columns' closing one.
var primaryKeyRE = regexp.MustCompile(`(?i)\)\s*PRIMARY\s+KEY\s*\(([^)]*)\)`)

// addChangeHistoryColumnRE finds the column a statement adds to the change history table.
var addChangeHistoryColumnRE = regexp.MustCompile("(?i)ALTER\\s+TABLE\\s+`?" + changeHistoryTable + "`?\\s+ADD\\s+COLUMN\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?")

// changeHistoryDDL is what the library declares for the change history table: its
// columns in order, and its key as keyForm writes it. source names the file they were
// read from.
type changeHistoryDDL struct {
	source  string
	columns []string
	key     string
}

func (changeHistory) Name() string {
	return changeHistoryName
}

func (changeHistory) Describe() string {
	return "the change history table carries the columns and the key of the resource package's statements"
}

// Meaning explains the obligation for the handoff brief.
func (changeHistory) Meaning() string {
	return "A resource that tracks its changes writes one record to `DataChangeEvents` for each create, update and delete, and each record carries the tenant of its row in `TenantId`. The library declares the table: `resource.DataChangeEventsDDL(resource.SpannerDBType, period)` renders its statements. The library writes a change record's columns by name and reads one record's history as one range of the table's key. A table short of a column fails every tracked write; a table with another key answers a record's history by reading every record the row ever had. Each line under the check is a column the application's migrations do not create, or a key that is not the library's. A missing column is added by a new migration (`ALTER TABLE DataChangeEvents ADD COLUMN`, with a default when the table already holds records), or in the migration that creates the table when no database has run it yet. A table's key cannot be altered: the migration that creates the table takes the library's statement, and a database made from the old statement is made again."
}

func (c changeHistory) Run(ctx context.Context, env *Env) Result {
	a := env.App
	p := a.Profile()
	if len(p.Sites) == 0 {
		return skip(c.Name(), "no site generator")
	}
	tables, unread, err := migrationTables(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	file, ok := tables[changeHistoryTable]
	if !ok {
		return skip(c.Name(), "no migration creates the "+changeHistoryTable+" table")
	}
	library, declared, problem, err := libraryChangeHistory(ctx, env)
	switch {
	case err != nil:
		return fail(c.Name(), err.Error())
	case problem != "":
		return fail(c.Name(), "1 change history problem(s)", append([]string{problem}, unread...)...)
	case !declared:
		// That library fills no column this check holds: the application has nothing
		// to fix.
		return skip(c.Name(), "the framework module the application pins declares no resource."+changeHistoryDDLFunc)
	}
	statements, err := upMigrationStatements(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	created, err := migrationStatement(a, file, changeHistoryTable)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	details := c.columnFindings(file, created, statements, library)
	if finding := c.keyFinding(file, created, library); finding != "" {
		details = append(details, finding)
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d change history problem(s)", len(details)), append(details, unread...)...)
	}

	return passWithDetails(c.Name(), "DataChangeEvents carries the columns and the key of the resource package's statements", unread...)
}

// columnFindings names each column of the library's table the application's table
// lacks: one its CREATE TABLE statement does not name as a whole word after the opening
// parenthesis, and no up migration adds.
func (changeHistory) columnFindings(file, created string, statements []string, library changeHistoryDDL) []string {
	body := created
	if i := strings.Index(created, "("); i >= 0 {
		body = created[i+1:]
	}
	var added []string
	for _, stmt := range statements {
		for _, m := range addChangeHistoryColumnRE.FindAllStringSubmatch(stmt, -1) {
			added = append(added, m[1])
		}
	}
	var details []string
	for _, column := range library.columns {
		if regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(column)+`\b`).MatchString(body) || namedFold(added, column) {
			continue
		}
		details = append(details, fmt.Sprintf("%s: the %s table has no %s column. Every tracked write fills the columns resource.%s declares (%s), so a tracked write to this table fails. Add the column in a new migration under %s; change the migration that creates the table instead if no database has run it yet.", file, changeHistoryTable, column, changeHistoryDDLFunc, library.source, path.Dir(file)))
	}

	return details
}

// keyFinding names a key of the application's table that is not the library's: the
// PRIMARY KEY of the statement that creates the table (read with its white space folded,
// by migrationStatement), as keyForm writes it, against the library's, letter case aside.
// A table's key cannot be altered, so no later migration changes it; a library statement
// that gives no key is compared with nothing.
func (changeHistory) keyFinding(file, created string, library changeHistoryDDL) string {
	key := tableKey(created)
	if library.key == "" || strings.EqualFold(key, library.key) {
		return ""
	}

	return fmt.Sprintf("%s: DataChangeEvents is keyed by (%s), and the resource package's statement keys it by (%s). A table's key cannot be altered: correct the migration that creates the table, and make again any database made from the old statement.", file, key, library.key)
}

// tableKey answers the key a CREATE TABLE statement gives its table, as keyForm writes
// it, or nothing for a statement that gives none.
func tableKey(stmt string) string {
	m := primaryKeyRE.FindStringSubmatch(stmt)
	if m == nil {
		return ""
	}

	return keyForm(m[1])
}

// keyForm writes a key's parts as the library does: backquotes dropped, the parts
// trimmed and joined by a comma and a space.
func keyForm(key string) string {
	parts := strings.Split(strings.ReplaceAll(key, "`", ""), ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}

	return strings.Join(parts, ", ")
}

// namedFold reports whether the list holds the name, case aside.
func namedFold(list []string, name string) bool {
	for _, item := range list {
		if strings.EqualFold(item, name) {
			return true
		}
	}

	return false
}

// libraryChangeHistory reads the Spanner statements of resource.DataChangeEventsDDL from
// the resource package of the framework module the application pins: the columns of the
// table's statement (the first word of each line between its first line and its
// ") PRIMARY KEY" line) and its key. declared is false when the
// package declares no such function: its version predates the library's change history
// statements. A module that cannot be found, and a function with no table statement, are
// the problem returned; a file that cannot be read is an error.
func libraryChangeHistory(ctx context.Context, env *Env) (ddl changeHistoryDDL, declared bool, problem string, err error) {
	dir, name, data, problem, err := libraryFunctionFile(ctx, env, changeHistoryDDLFunc)
	switch {
	case err != nil:
		return changeHistoryDDL{}, false, "", err
	case problem != "":
		return changeHistoryDDL{}, false, problem, nil
	case data == nil:
		return changeHistoryDDL{}, false, "", nil
	}
	raw, err := spannerDDL(name, data, changeHistoryDDLFunc)
	if err != nil {
		return changeHistoryDDL{}, true, "", err
	}
	ddl = changeHistoryDDL{source: path.Join(path.Base(dir), name)}
	table := false
	for _, stmt := range raw {
		if m := createTableRE.FindStringSubmatch(stmt); len(m) > 1 && m[1] == changeHistoryTable {
			table = true
			ddl.columns = tableColumns(stmt)
			ddl.key = tableKey(stmt)
		}
	}
	if !table {
		return changeHistoryDDL{}, true, fmt.Sprintf("%s in %s declares no CREATE TABLE statement for %s, so the schema cannot be compared with it", changeHistoryDDLFunc, filepath.Join(dir, name), spannerDBTypeIdent), nil
	}

	return ddl, true, "", nil
}

// tableColumns answers the first word of each line of a CREATE TABLE statement between
// its first line and its ") PRIMARY KEY" line: the table's columns, as the library writes
// the statement.
func tableColumns(stmt string) []string {
	lines := strings.Split(stmt, "\n")
	var columns []string
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ")") {
			break
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			columns = append(columns, fields[0])
		}
	}

	return columns
}

// upMigrationStatements reads the up migrations under every site's file:// migration
// source and answers their statements, comments stripped, file by file in name order.
func upMigrationStatements(a *app.App, p app.Profile) ([]string, error) {
	var statements []string
	for _, dir := range migrationSourceDirs(p) {
		entries, err := os.ReadDir(a.Abs(dir))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.Wrap(err, "os.ReadDir()")
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
				continue
			}
			data, err := os.ReadFile(a.Abs(path.Join(dir, e.Name())))
			if err != nil {
				return nil, errors.Wrap(err, "os.ReadFile()")
			}
			statements = slices.AppendSeq(statements, strings.SplitSeq(stripSQLComments(string(data)), ";"))
		}
	}

	return statements, nil
}
