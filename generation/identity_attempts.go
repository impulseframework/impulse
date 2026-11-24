package generation

import (
	"strings"
)

// What a list says of the counts an auth keeps of failed sign-ins. A row of the sign-in
// attempts table is keyed by a name a person typed, or, for one of the session library's
// own counts, by a kind in capitals, a colon and what it counts (an address, or the id of
// an account or of a mailed link); whether it is locked depends on the database's clock,
// and how many failures lock a name is the Auth's, which no table holds. The generated
// management therefore declares the counts a second time, as a view beside the table's
// declaration (<Auth>ListedSignInAttempt), which holds only a count that is locked or
// still being counted, says what each counts and whose it is, and which a page lists.

// listedSignInAttemptSuffix is the suffix of the view over the sign-in attempts, after the
// auth's name.
const listedSignInAttemptSuffix = "ListedSignInAttempt"

// signInFailureLimitParam is the query parameter the list of the failed sign-ins reads
// the Auth's limit from: how many failed sign-ins lock a name. The list asks the Auth at
// each query, through the function the auth's generated identity client hands it.
const signInFailureLimitParam = "signInFailureLimit"

// signInAttemptKinds are the session library's own counts in the sign-in attempts table,
// by the prefix of their keys (the session library's attempts.OwnKey), with the word the
// list says each by. A key with none of them is a name's count, signIn.
var signInAttemptKinds = []struct{ prefix, kind string }{
	{prefix: "MAIL:", kind: "mail"},
	{prefix: "CODE:", kind: "code"},
	{prefix: "RESET:", kind: "reset"},
	{prefix: "ADDRESS-GUESS:", kind: "addressGuess"},
	{prefix: "INVITATION:", kind: "invitationCode"},
	{prefix: "INVITATION-GUESS:", kind: "invitationGuess"},
	{prefix: "CODE-GUESS:", kind: "codeGuess"},
	{prefix: "FIRST-PASSWORD:", kind: "firstPassword"},
}

// ListedSignInAttempt is the struct of the failed sign-ins as a list shows them, each
// saying what it counts and whose it is (CrewListedSignInAttempt; its list is
// CrewListedSignInAttempts).
func (d *identitySourceData) ListedSignInAttempt() string {
	return d.auth + listedSignInAttemptSuffix
}

// SignInFailureLimitParam is signInFailureLimitParam for the list's template.
func (d *identitySourceData) SignInFailureLimitParam() string {
	return signInFailureLimitParam
}

// AccountsList is the struct of the whole application's list of the accounts, the one a
// page lists them from (pageLists): the list with what each holds where the schema holds
// what it reads, and the declaration over the users table where it does not. A list's
// field that names an account offers that list's rows.
func (d *identitySourceData) AccountsList() string {
	if d.listsHeldRoles() {
		return d.ListedUser()
	}

	return d.User()
}

// SignInAttemptKind is the SQL of the column that says what a count is kept for,
// attempt being the alias of the row: the first of the session library's own kinds whose
// prefix the key starts with, compared as written so a capital tells it apart, and
// signIn for any other key, a name's. A name is counted under its normalized form, which
// holds no ASCII capital, so a name typed at the sign-in form that reads like a key is a
// signIn row and never another kind's.
func (d *identitySourceData) SignInAttemptKind(attempt string) string {
	lines := make([]string, 0, len(signInAttemptKinds)+3)
	lines = append(lines, "CASE")
	for _, k := range signInAttemptKinds {
		lines = append(lines, "      WHEN STARTS_WITH("+attempt+".Name, '"+k.prefix+"') THEN '"+k.kind+"'")
	}
	lines = append(lines, "      ELSE 'signIn'", "    END AS Kind")

	return "    " + strings.Join(lines, "\n")
}

// signInAttemptListSources is the source file of the list of the failed sign-ins: a view
// in the declarations package, named as the views beside the permission store's tables
// are, written where the schema holds the sign-in attempts table.
func (r *resourceGenerator) signInAttemptListSources(inst *identityInstance) []identitySourceFile {
	return []identitySourceFile{{
		dir:      inst.resources,
		name:     fileStem(r.pluralize(inst.auth + listedSignInAttemptSuffix)),
		template: identityListedSignInAttemptsTemplate,
		needs:    []identityTable{identitySignInAttempts},
	}}
}
