package check

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// deleteTokens verifies the delete tokens table: the DeleteTokens table, where the
// question a request's delete asks first leaves its token, is created by a migration of
// the application and carries every column the resource package of the framework module
// the application pins declares for it (resource.DeleteTokensDDL, read from the module's
// source as change-history reads its statements), created with the table or added by a
// later migration, and the statement that creates the table keys it as the library's
// does, spacing, backquotes and letter case aside. A question that passes writes exactly
// the library's columns, and a token is read by its id alone.
type deleteTokens struct{}

// deleteTokensName is the check's name.
const deleteTokensName = "delete-tokens"

// The function that declares the delete tokens statements, and the table they create.
const (
	deleteTokensDDLFunc = "DeleteTokensDDL"
	deleteTokensTable   = "DeleteTokens"
)

// addDeleteTokensColumnRE finds the column a statement adds to the delete tokens table.
var addDeleteTokensColumnRE = regexp.MustCompile("(?i)ALTER\\s+TABLE\\s+`?" + deleteTokensTable + "`?\\s+ADD\\s+COLUMN\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?`?([A-Za-z_][A-Za-z0-9_]*)`?")

func (deleteTokens) Name() string {
	return deleteTokensName
}

func (deleteTokens) Describe() string {
	return "the delete tokens table carries the columns and the key of the resource package's statements"
}

// Meaning explains the obligation for the handoff brief.
func (deleteTokens) Meaning() string {
	return "A request that deletes a record with rows under it is asked first, and the question's answer carries a token the delete brings back. The library keeps the tokens in `DeleteTokens`: `resource.DeleteTokensDDL(resource.SpannerDBType)` renders its statements, and an application copies them into a migration."
}

func (c deleteTokens) Run(ctx context.Context, env *Env) Result {
	a := env.App
	p := a.Profile()
	if len(p.Sites) == 0 {
		return skip(c.Name(), "no site generator")
	}
	library, declared, problem, err := libraryTable(ctx, env, deleteTokensDDLFunc, deleteTokensTable)
	switch {
	case err != nil:
		return fail(c.Name(), err.Error())
	case problem != "":
		return fail(c.Name(), "1 delete token problem(s)", problem)
	case !declared:
		// That library writes no token: the application has nothing to create.
		return skip(c.Name(), "the framework module the application pins declares no resource."+deleteTokensDDLFunc)
	}
	tables, unread, err := migrationTables(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	file, ok := tables[deleteTokensTable]
	if !ok {
		finding := fmt.Sprintf("no migration creates the %s table, where the question a delete asks first leaves its token; copy its statements from resource.%s(resource.%s) into a new migration under %s", deleteTokensTable, deleteTokensDDLFunc, spannerDBTypeIdent, migrationsDir(p))

		return fail(c.Name(), "1 delete token problem(s)", append([]string{finding}, unread...)...)
	}
	statements, err := upMigrationStatements(a, p)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	created, err := migrationStatement(a, file, deleteTokensTable)
	if err != nil {
		return fail(c.Name(), err.Error())
	}
	var details []string
	for _, column := range columnsLacking(created, statements, addDeleteTokensColumnRE, library.columns) {
		details = append(details, fmt.Sprintf("%s: the %s table has no %s column. A delete's question fills the columns resource.%s declares (%s), so every delete of a record with rows under it fails. Add the column in a new migration under %s; change the migration that creates the table instead if no database has run it yet.", file, deleteTokensTable, column, deleteTokensDDLFunc, library.source, path.Dir(file)))
	}
	if key := tableKey(created); library.key != "" && !strings.EqualFold(key, library.key) {
		details = append(details, fmt.Sprintf("%s: %s is keyed by (%s), and the resource package's statement keys it by (%s). A table's key cannot be altered: correct the migration that creates the table, and make again any database made from the old statement.", file, deleteTokensTable, key, library.key))
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d delete token problem(s)", len(details)), append(details, unread...)...)
	}

	return passWithDetails(c.Name(), "DeleteTokens carries the columns and the key of the resource package's statements", unread...)
}
