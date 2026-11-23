// Package advise writes the brief impulse advise hands to an agent: what the application's
// generate programs raised under -audit (or where the accepted warnings are pinned, when
// generation is skipped), where the accepted role warnings are pinned, impulse's own
// deterministic reading of each kind, the question each kind leaves to judgment, and the
// schema design no tool judges, which the agent reviews whether or not anything was
// raised. The agent answers in prose and edits nothing; impulse reads no test file itself.
//
// Analysis by an agent is opt-in, a command of its own beside impulse audit: the check
// gates every change, the audit is read by decision, and advice is asked for.
package advise

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/audit"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
)

// File is the root-relative path of the brief. It is transient like the handoff's:
// written by the command, removed after an agent run, and never committed.
const File = ".impulse-advice.md"

// warningsTest is the file a program in the runner shape pins its accepted warnings in,
// beside the program.
const warningsTest = "warnings_test.go"

// Brief is what the agent is told.
type Brief struct {
	App *app.App
	// Options is the options check's result: the option set in force, when it passed.
	Options check.Result
	// Programs are the generate programs with what each raised, or, under SkipGenerate,
	// with the test that pins their accepted warnings.
	Programs []Program
	// SkipGenerate reports that no program was run: the agent reads the pinned warnings.
	SkipGenerate bool
	// RoleTest is the root-relative test that pins the accepted role warnings, and
	// RoleTestPresent whether it exists.
	RoleTest        string
	RoleTestPresent bool
}

// Program is one generate program in the brief.
type Program struct {
	// File is the program's declaring file, root-relative; Dir the directory it runs from.
	File string
	Dir  string
	// Lines are the Warning: and Audit: lines it raised, when it was run.
	Lines []string
	// WarningsTest is the root-relative test pinning its accepted warnings, and
	// WarningsTestPresent whether it exists.
	WarningsTest        string
	WarningsTestPresent bool
}

// Collect gathers the brief's facts: the option set, each program's lines under -audit
// (or, when skipGenerate, the test that pins them), and the role warnings test. A program
// that fails, or does not take -audit, is an error: the brief would miss its input, and
// the message is impulse audit's.
func Collect(ctx context.Context, env *check.Env, skipGenerate bool) (*Brief, error) {
	a := env.App
	if len(a.Generators) == 0 {
		return nil, fmt.Errorf("no generator program found (no file calls generation.NewResourceGenerator)")
	}
	b := &Brief{App: a, SkipGenerate: skipGenerate, RoleTest: check.ValidationTestFile(a)}
	b.RoleTestPresent = exists(a, b.RoleTest)
	options, err := check.Select([]string{"options"})
	if err != nil {
		return nil, err
	}
	b.Options = check.Run(ctx, env, options)[0]

	var ran []audit.Program
	if !skipGenerate {
		if env.Out != nil {
			fmt.Fprintln(env.Out, "running each generate program with -audit (needs the Spanner emulator)")
		}
		ran = audit.Collect(ctx, a, env.Exec)
	}
	for i, g := range a.Generators {
		p := Program{File: g.File, Dir: a.ProgramDir(g)}
		p.WarningsTest, p.WarningsTestPresent = warningsTestOf(a, g)
		if !skipGenerate {
			switch r := ran[i]; {
			case r.NoAudit:
				return nil, fmt.Errorf("%s: the program does not take -audit; adopt the runner shape (README, impulse audit), or pass --skip-generate to read its pinned warnings instead", r.File)
			case r.Failed:
				return nil, fmt.Errorf("%s: %s failed:\n%s", r.File, r.Command(), strings.Join(r.Tail, "\n"))
			default:
				p.Lines = r.Lines
			}
		}
		b.Programs = append(b.Programs, p)
	}

	return b, nil
}

// warningsTestOf names the test that pins a program's accepted warnings: beside the file
// declaring the generator (the skeletons keep the declaration, the runner, and the test
// in one directory; an application that declares in one package and runs it from a main
// package beside keeps the test with the declaration), or beside the runner, whichever
// exists; when neither does, the declaration's, as the file to write.
func warningsTestOf(a *app.App, g *app.Generator) (rel string, present bool) {
	declaring := path.Join(path.Dir(g.File), warningsTest)
	for _, candidate := range []string{declaring, path.Join(a.ProgramDir(g), warningsTest)} {
		if exists(a, candidate) {
			return candidate, true
		}
	}

	return declaring, false
}

func exists(a *app.App, rel string) bool {
	_, err := os.Stat(a.Abs(rel))

	return err == nil
}

// Warnings counts the Warning: lines across the programs, and Findings the Audit: lines.
func (b *Brief) Warnings() int { return b.count(audit.WarningPrefix) }

// Findings counts the Audit: lines across the programs.
func (b *Brief) Findings() int { return b.count(audit.AuditPrefix) }

func (b *Brief) count(prefix string) int {
	n := 0
	for i := range b.Programs {
		for _, line := range b.Programs[i].Lines {
			if strings.HasPrefix(line, prefix) {
				n++
			}
		}
	}

	return n
}

// Write renders the brief as Markdown.
func (b *Brief) Write(w io.Writer) {
	fmt.Fprintf(w, "# Impulse advice: %s\n\n", b.appName())
	fmt.Fprintf(w, "You are reading an Impulse application (Go services built on the Impulse Framework, with its resource package generating the handlers, routes, and browser clients from annotated resource structs). The application root is `%s`, and it is your working directory. impulse gathered what the application's schema and roles raised and reads each kind the same way every time; what no rule decides is the judgment each kind leaves to the application, and that is what you are asked for. Answer in prose. Edit nothing.\n\n", b.App.Root)

	if b.Options.Status == check.Pass {
		fmt.Fprintf(w, "## The option set in force\n\n%s\n", b.Options.Summary)
		for _, d := range b.Options.Details {
			fmt.Fprintf(w, "- %s\n", d)
		}
		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "## What the application raised\n\n")
	if b.SkipGenerate {
		fmt.Fprintf(w, "Generation was skipped, so the programs were not run. Each program pins the warnings it has accepted as typed values in its warnings test; read them there. An audit finding is never pinned: it is printed only when a program runs with `-audit`, so none is listed here.\n\n")
	} else {
		fmt.Fprintf(w, "Each generate program was run with `-audit`. A `Warning:` line is a schema warning the program raises on every run; an `Audit:` line is an advisory finding it raises only when asked.\n\n")
	}
	for i := range b.Programs {
		p := &b.Programs[i]
		fmt.Fprintf(w, "### %s (`go run ./%s -audit`)\n\n", p.File, p.Dir)
		switch {
		case b.SkipGenerate:
			fmt.Fprintf(w, "Accepted warnings: %s\n\n", b.pointer(p.WarningsTest, p.WarningsTestPresent, "the program is not in the runner shape (no warnings test beside it); run `impulse advise` without `--skip-generate` to read what it raises"))
		case len(p.Lines) == 0:
			fmt.Fprintf(w, "No warnings or findings.\n\n")
		default:
			for _, line := range p.Lines {
				fmt.Fprintf(w, "- %s\n", line)
			}
			fmt.Fprintf(w, "\nThe accepted set is pinned in %s.\n\n", b.pointer(p.WarningsTest, p.WarningsTestPresent, "no warnings test beside the program; a warning above is accepted nowhere in code yet"))
		}
	}

	fmt.Fprintf(w, "## Accepted role warnings\n\n")
	fmt.Fprintf(w, "The deploy-time validation of the role files (`access.ValidateRoles`) prints a warning per grant it provisions as written but flags. The application accepts one by pinning its typed value in %s; each row of that test is one role file, and an empty expected set means nothing is accepted. Read the pinned values there: they are role warnings to advise on too.\n\n",
		b.pointer(b.RoleTest, b.RoleTestPresent, "no such test exists yet, so no role warning is accepted in code; `impulse check` (auths-wired) says where it belongs"))

	fmt.Fprintf(w, "## How impulse reads each kind\n\n")
	fmt.Fprintf(w, "A line's kind is told by its wording. The reading is impulse's and fixed; the question is yours.\n\n")
	for _, k := range kinds {
		fmt.Fprintf(w, "**%s** (%s). %s\n\n*Question:* %s\n\n*Edits on offer:* %s\n\n", k.name, k.recognize, k.reading, k.question, k.edits)
	}
	fmt.Fprintf(w, "A line of a kind not listed here is read as written.\n\n")

	fmt.Fprintf(w, "## What no tool judges\n\n")
	fmt.Fprintf(w, "Some of a schema's design is the application's choice, and no line above speaks of it: the generator and `impulse check` take it as it is declared. Read the migrations and the resource structs for each choice below, whether or not anything was raised.\n\n")
	for _, c := range choices {
		fmt.Fprintf(w, "**%s** (%s). %s\n\n*Design fault:* %s\n\n*Question:* %s\n\n*Edits on offer:* %s\n\n", c.name, c.read, c.reading, c.fault, c.question, c.edits)
	}

	fmt.Fprintf(w, "## Rules\n\n")
	for i, rule := range rules {
		fmt.Fprintf(w, "%d. %s\n", i+1, rule)
	}
	fmt.Fprintf(w, "\n## Done when\n\nYour answer is written. There is nothing to run and nothing to verify: nothing was to be changed.\n")
}

// String renders the brief as Markdown.
func (b *Brief) String() string {
	var sb strings.Builder
	b.Write(&sb)

	return sb.String()
}

func (b *Brief) appName() string {
	if b.App.GoMod != nil && b.App.GoMod.Module != nil {
		return b.App.GoMod.Module.Mod.Path
	}

	return b.App.Root
}

// pointer names a file the agent reads, or says why it cannot.
func (b *Brief) pointer(rel string, present bool, absent string) string {
	if present {
		return "`" + rel + "`"
	}

	return "`" + rel + "` (absent: " + absent + ")"
}

// kind is impulse's reading of one warning or finding kind, with how a line of it reads,
// the question it leaves to judgment, and the edits that answer it.
type kind struct {
	name      string
	recognize string
	reading   string
	question  string
	edits     string
}

// kinds are the warning and finding kinds the framework raises today: the generator's
// four schema warnings, the audit pass's one finding, and the deploy-time validation's
// two role warnings.
var kinds = []kind{
	{
		name:      "Index warning",
		recognize: "`Warning: <Resource> lists in <columns> order with no index leading with those columns ...; wanted: CREATE INDEX ...`",
		reading:   "A listed tenant-scoped resource declares an `@order` that no index serves, so Spanner drives every page of its list from the tenant column's foreign-key index and sorts the tenant's whole partition each time. The line carries the `CREATE INDEX` the resource wants, verbatim; a migration adding it removes the warning, and nothing else in the application changes.",
		question:  "Whether the list pages at volume. A table that stays at tens of rows per tenant never feels the sort; a table that grows does, on every page, and the index is cheapest to add while the table is small.",
		edits:     "the migration with the `CREATE INDEX` from the line; or the pinned acceptance in the program's warnings test for a table that stays small, said so in its doc.",
	},
	{
		name:      "Join-path warning",
		recognize: "`Warning: <Resource> resolves its tenant through <column, table.column, ...>, so its lists scan all of <Table>, every tenant ...`",
		reading:   "A listed resource finds its tenant through `@domain(via: ...)`, a path of foreign keys, so no column on its own row names the tenant: every list scans the whole table across every tenant with a lookup per row up the path, and no index on the table changes that. The way out is a tenant column on the row (`@domain` on it), which is a schema change: a column, a backfill, and the annotation.",
		question:  "Whether the table will grow, and whether the tenant-column migration is worth doing now, while the table is small and the backfill is trivial, or the scan is acceptable for the table's expected size.",
		edits:     "the tenant column and its backfill migration, with `@domain` moved onto it; or the pinned acceptance for a table that stays small.",
	},
	{
		name:      "Subject set index warning",
		recognize: "`Warning: <Resource> declares subject.<set>, which finds the requester's rows by <column> with no index leading with <columns> ...; wanted: CREATE INDEX ...`",
		reading:   "A `@subjectSet` finds the requester's rows of its table by the column the line names: the column the annotation sits on, or one of the columns its `who:` names, and a set whose `who:` names several columns raises a line for each. The lookup binds the tenant column where the row carries one, that column, and the value the set yields, and no index of the table leads with them, so every check of a condition that tests membership in the set reads the whole table. The primary key counts, and so does a secondary index with the key columns Spanner appends to it; a null-filtered index and a search index do not.",
		question:  "Whether the set's table grows. A table of a few rows a tenant never feels the read; a table of memberships or assignments, which grows with the people and with what they are assigned, does, on every request that checks the set.",
		edits:     "the migration with the `CREATE INDEX` from the line; or the pinned acceptance in the program's warnings test for a table that stays small, said so in its doc.",
	},
	{
		name:      "Enumeration size warning",
		recognize: "`Warning: <Type> enumerates <n> rows of <Table>, above the <limit> a baked table may hold ...`",
		reading:   "An `@enumerate` table is baked into the TypeScript metadata of every field keyed into it, one copy per field; the line gives the rows, the bytes, and the fields. Above the limit the browser carries that on every load. The alternative is a runtime resource: drop `@enumerate` from the type (the generated constants go with it) and expose the table with a `@resource` struct, whose picker reads it whole or paged under a `@page` maximum.",
		question:  "Whether the table is really an enumeration, a fixed short list the code names by constant, or a reference table that grows and should be a resource with a picker.",
		edits:     "the `@resource` struct and the `@enumerate` removal (and the code that used its constants); or the pinned acceptance for a list that is fixed and merely long.",
	},
	{
		name:      "Grant warning (a role warning, pinned in the deploy test)",
		recognize: "`role <Role>: <Permission> on <Resource> is granted under \"<condition>\" without Read or List on <Row> ...`",
		reading:   "A role holds a conditional Delete, Update, or targeted Execute on a row it can neither Read nor List. A caller holding the role learns from a Forbidden answer that the row exists, where a read would have answered NotFound. Granting Read or List on the row resource in this role, or in a role assigned with it, closes it; a Read whose condition is narrower than the write's leaks the same way.",
		question:  "Whether the disclosure is intended: whether the row's existence is sensitive to this role, or the role is a writer who knows the rows anyway.",
		edits:     "a Read or List grant on the row resource in the role file, with a condition at least as wide as the write's; or the pinned `access.GrantWarning` in the deploy test.",
	},
	{
		name:      "Concealing key warning (a role warning, pinned in the deploy test)",
		recognize: "`role <Role>: List on <Resource>.<field> is granted under \"<condition>\", and <field> is the default order|a sort or filter key of <Resource> whose masked cells conceal ...`",
		reading:   "A role lists a field under a condition, the field's masked cells conceal, and the role's other List grants on the resource leave the field's condition standing in the query, so a page ordered or filtered by the field sorts the tenant's whole partition on a CASE no index serves. Read from the line: whether the field is `the default order` (every page this role lists pays) or `a sort or filter key` (only a caller who asks for that sort or filter pays); and why the CASE stands, because the role `lists other fields unconditionally` or `also lists fields under` conditions the field's own does not cover. A table that never pages at volume does not care.",
		question:  "Whether the field's rank is sensitive (whether disclosing where a hidden value sorts gives the role something it should not have), and whether the table will page at volume for this role.",
		edits:     "the field granted unconditionally in this role, when its values are not sensitive to the role; `masking:\"positional\"` on the field, disclosing where hidden values fall in an order, when the rank is not; or the pinned `access.ConcealingKeyWarning` in the deploy test for a table that never pages at volume.",
	},
}

// choice is a part of a schema's design that is the application's choice and that no tool
// judges, with where the agent reads it, impulse's reading of both designs, the design
// fault the agent looks for, the question it leaves to judgment, and the edits that answer
// it.
type choice struct {
	name     string
	read     string
	reading  string
	fault    string
	question string
	edits    string
}

// choices are the parts of a schema's design no tool judges today: how the records of a
// tenant link to one another.
var choices = []choice{
	{
		name:     "Links between a tenant's records",
		read:     "read every `FOREIGN KEY` and `INTERLEAVE IN PARENT` of the migrations that joins two tables whose resources carry `@domain`",
		reading:  "A record keeps its tenant in a column of its own (a bare `@domain`) or reads it through the record it is under (`@domain(via: ...)`). Between two records that each keep the column, a foreign key is written in one of two ways, and which one is the application's choice, link by link. Say a company is the tenant, with projects and vehicles. A key that holds the tenant, the tenant column of the one beside the tenant column of the other (`FOREIGN KEY (ProjectId, CompanyId) REFERENCES Projects (Id, CompanyId)`), has the database refuse every row that names another tenant's row, whoever writes it: it is the key for a record that must never be under another tenant's record. A key on the other row's id alone (`FOREIGN KEY (ProjectId) REFERENCES Projects (Id)`) leaves the link free. The database holds the row to a record that exists and to nothing more, and the library holds the row being written to the request's tenant without looking whose row its foreign key names. It is the key for a record the application may move between tenants, and it lets a caller who knows the id of another tenant's row name that row. A child interleaved in its parent is linked by the parent's key, so the link holds the tenant only where that key does. A record that reads its tenant through other rows has no tenant column to put in a key: the library proves the link its tenant is read through when the row is written, and no other link of the record.",
		fault:    "A record that can link a row of one tenant to a row of another: one with two links that nothing holds to one tenant, whether two foreign keys on an id alone or, on a record that reads its tenant through other rows, a foreign key beside the link its tenant is read through. No tool looks for it.",
		question: "For each link, whether the record is ever meant to be moved to another tenant or to stand under another tenant's record. Where nothing in the code moves it or attaches it across tenants, the key that holds the tenant closes the link; where the application does, the free link is the design. For each record with two or more links to a tenant's records, whether the schema holds them to one tenant.",
		edits:    "the migration that rewrites a foreign key to hold the tenant column of each table, in the order of the key it references (the generator prints the line for another order), for a record that must never be under another tenant's record; no edit for a link the application means to leave free, said so in the resource's doc; for a design fault, the record and its links named, with the keys that would hold them to one tenant where each table keeps the tenant column, and where one does not, the choice put to the developer as a question.",
	},
}

// rules are the agent's rules: an answer, no edit, no write.
var rules = []string{
	"Answer per warning and finding, in prose: name it, apply the reading above to it (read the resource struct, its migrations, the role file, and how the table is used, to say which branch of the reading holds), answer the question from what the code tells you, and name the edit you recommend, from the edits on offer or better, or say the warning should be accepted and where its pin goes (the program's warnings test for a schema warning, the deploy test for a role warning). The pinned values in those tests are warnings to advise on too.",
	"Answer on each choice under \"What no tool judges\" too, whether or not anything was raised: for each link, say which design it is and whether that fits how the code uses the record, and name every design fault you find, with its table and its links.",
	"Edit nothing. Run nothing that writes: no `go generate`, no `impulse` command, no test. Read the code.",
	"Where the code cannot answer the question (whether a table will grow, whether a field's rank is sensitive to a role), say so and state the question for the developer in one sentence.",
	"Finish with the edits you recommend as a list, in the order you would make them, each naming its file.",
}
