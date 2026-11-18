package resource

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/tracer"
)

// DirectoryOnlyRoute is the route of the read of a tenant's directory-only switch as it
// stands (DirectoryOnlyState), under the tenant's own route:
// GET <prefix>/<tenants>/{tenant}/directory-only. The generated router registers it
// beside the two methods that turn the switch (Make<Record>DirectoryOnly and
// End<Record>DirectoryOnly) on every outlet that serves sessions and the tenant record,
// for a record that declares the switch.
const DirectoryOnlyRoute = "directory-only"

// DirectoryOnlyWord is the word a person types before a tenant is made directory only
// (the warning of Make<Record>DirectoryOnly): the tenant is closed to every sign-in but
// one through its directory. A warning's word is one lowercase word, which a
// directory's name is not, so the word is the same for every tenant.
const DirectoryOnlyWord = "close"

// DirectoryOnlyState is a tenant's directory-only switch as it stands.
type DirectoryOnlyState struct {
	DirectoryOnly bool `json:"directoryOnly"`
}

// DirectoryOutsider is a person who holds a seat in a tenant, has never signed in
// through the tenant's directory and has no other way in: someone the tenant's
// directory-only switch keeps out for as long as it is on.
type DirectoryOutsider struct {
	// UserID is the id of the person's account, and Username what identifies them.
	UserID   impulse.UUID `json:"userId"`
	Username string       `json:"username"`
	// Name is the person's name as people read it, absent where the account holds none.
	Name *string `json:"name,omitempty"`
}

// DirectoryOutsiders reads the people of one auth who hold a seat in tenant, in the
// tenant itself or in every tenant, and have no sign-in through the tenant's directory,
// in the caller's transaction. A super administrator is left out: no tenant's sign-in
// settings shut them out. So is the tenant's own administrator who has a password:
// they enter with it and a second factor (signin.TenantDeclaration.AdmitsAdministrator),
// and lose nothing. The generated identity client of each auth whose people a
// tenant seats supplies it (DirectoryOutsiders), over the tenant's own list of its
// people; an application names none of them.
type DirectoryOutsiders func(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) ([]DirectoryOutsider, error)

// DirectoryPeople is one auth's part of what turning a tenant's directory-only switch
// on would do: the reader of the people it would keep out, and the tenant's list of
// that auth's people, which decides whether the caller is told who they are. The
// warning names people only to someone who may list them: a caller who holds List, in
// the tenant, on the list and, on no condition, on each field of it that names a
// person. Anyone else is told how many there are and nobody's name. A reader with no
// list, or with no field named, names nobody.
type DirectoryPeople struct {
	// List is the tenant's list of the auth's people as grants name it, and Fields the
	// fields of it that name a person: the ones the warning lists a person by
	// (DirectoryOutsider).
	List   accesstypes.Resource
	Fields []accesstypes.Tag
	// Outsiders reads the people the switch would keep out.
	Outsiders DirectoryOutsiders
	// AdministratorsEnter says the auth's people keep the administrators' way in: the
	// auth has a password method and names the roles that administer a tenant, so the
	// tenant's own administrator who has a password still enters, with a code mailed to
	// them. The warning says so, since it is the one other way into the tenant.
	AdministratorsEnter bool
}

// DirectoryOnly holds a write of tenant's directory-only switch to its rules, in the
// caller's transaction, and writes nothing: value is the flag the write carries. Turning
// the switch off is plain, and so is a write that leaves it on. Turning it on is
// refused, each time as a Conflict with a sentence the person can act on:
//
//   - until the tenant has its directory: with none there is nothing to be only of, and
//     nobody could enter;
//   - unless the caller is signed in through that directory themselves: the session
//     proves the directory works, and the person who closes the tenant to every other
//     sign-in is not shut out by it;
//   - unless the caller's own account has a password, where their auth has a password
//     method (PasswordsBy): a tenant's own administrator enters a directory-only
//     tenant with a password and a second factor, which is the way back in if the
//     directory has a problem, and a person who has only ever signed in through the
//     directory would have none. The refusal says to choose a password first. Where
//     the caller's auth has no password method the rule stands down, and nothing is
//     said of it.
//
// The rules read what is committed, and a transaction's reads do not see what it has
// buffered, so the transaction notes each write the rules pass (directoryWrites): a
// request that removes the tenant's directory and turns the switch on is refused,
// whichever comes first, as each would be after the other had committed.
//
// Nobody is exempt from them, a super administrator included: the rules are about the
// directory and the way back in, not about who may enter. The switch is the tenant's own, turned by the tenant's
// administrator: the generated methods of a tenant record that declares it
// (@directoryOnly) and a directories record, Make<Record>DirectoryOnly and
// End<Record>DirectoryOnly in the tenant's scope, ask the rule once the caller's Execute
// grant has answered and then write the flag. No other request writes it: the record's
// field is output only on the wire, so neither the record's update nor its create
// carries it.
func (r *TenantDirectoryRules) DirectoryOnly(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, value any) error {
	writes := notedDirectoryWrites(txn)
	if !turnsOn(value) {
		writes.noteTurnedOff(tenant)

		return nil
	}
	on, _, err := r.switchState(ctx, txn, tenant)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	if writes.removesDirectory(tenant) {
		return httpio.NewConflictMessagef("This same request removes this %[1]s's directory, so it cannot also turn directory only on: nobody could enter the %[1]s. Keep the directory, or leave directory only off.", r.word)
	}
	if _, err := r.turnOn(ctx, txn, tenant); err != nil {
		return err
	}
	writes.noteTurnedOn(tenant)

	return nil
}

// directoryWrites notes what one transaction's writes do to tenants' directories and
// directory-only switches. The rules of both read what is committed, and a
// transaction's reads do not see its own buffered writes, so two writes of one request
// could each pass a rule the other breaks: the switch turned on beside the removal of
// the directory it is only of, which would leave a tenant nobody enters. The rules note
// each such write they pass and hold the next one of the transaction to it. A nil note,
// a transaction that keeps none, remembers nothing and refuses nothing.
type directoryWrites struct {
	// turnedOn are the tenants a write of the transaction turns directory only.
	turnedOn map[accesstypes.Domain]bool
	// removed are the tenants whose directory a write of the transaction removes.
	removed map[accesstypes.Domain]bool
}

// newDirectoryWrites is the empty note a transaction starts with.
func newDirectoryWrites() *directoryWrites {
	return &directoryWrites{turnedOn: map[accesstypes.Domain]bool{}, removed: map[accesstypes.Domain]bool{}}
}

// directoryWriteNoter is a transaction that keeps the note: the library's own Spanner
// transaction, one note for each attempt, so a retried transaction starts with none.
type directoryWriteNoter interface {
	directoryWrites() *directoryWrites
}

// notedDirectoryWrites answers txn's note, nil for a transaction that keeps none.
func notedDirectoryWrites(txn ReadWriteTransaction) *directoryWrites {
	noter, ok := txn.(directoryWriteNoter)
	if !ok {
		return nil
	}

	return noter.directoryWrites()
}

// noteTurnedOn notes that a write of the transaction turns tenant directory only.
func (w *directoryWrites) noteTurnedOn(tenant accesstypes.Domain) {
	if w != nil {
		w.turnedOn[tenant] = true
	}
}

// noteTurnedOff notes that a write of the transaction leaves tenant's switch off, which
// takes back an earlier write of the same transaction that turned it on.
func (w *directoryWrites) noteTurnedOff(tenant accesstypes.Domain) {
	if w != nil {
		delete(w.turnedOn, tenant)
	}
}

// noteRemoved notes that a write of the transaction removes tenant's directory.
func (w *directoryWrites) noteRemoved(tenant accesstypes.Domain) {
	if w != nil {
		w.removed[tenant] = true
	}
}

// turnsDirectoryOnly reports whether a write of the transaction turns tenant directory only.
func (w *directoryWrites) turnsDirectoryOnly(tenant accesstypes.Domain) bool {
	return w != nil && w.turnedOn[tenant]
}

// removesDirectory reports whether a write of the transaction removes tenant's directory.
func (w *directoryWrites) removesDirectory(tenant accesstypes.Domain) bool {
	return w != nil && w.removed[tenant]
}

// DirectoryOnlyWarning says what turning tenant's directory-only switch on would do, in
// the caller's transaction, and writes nothing: the warning the method that turns it on
// (Make<Record>DirectoryOnly) answers before it is run, which a dialog shows and holds
// its button under until the person has typed DirectoryOnlyWord. The consequence says
// who will be able to enter the tenant and how many of the people seated in it will
// not, counted through people, one entry for each auth whose people the tenant seats;
// the confirm words say what the button does. Where no entry reads anybody (an
// application whose people no generated management lists) nobody was counted, and the
// sentence says who will lose access without a number: it never says that everyone has
// signed in through the directory on a count nobody made.
//
// Everyone the switch would keep out is counted. A person is named only to a caller who
// may list them: the people of an auth are named where the request's caller
// (CallerFrom) holds the tenant's list of that auth's people (DirectoryPeople), and
// counted alone where the caller does not. A call outside a request names nobody.
//
// It refuses as DirectoryOnly refuses, with the same sentences, so a page that asks
// first shows the refusal instead of a dialog; a tenant the record does not hold is
// NotFound. A tenant that is directory only already has nothing to turn on, and the
// warning is refused as a Conflict that says so and names neither its directory nor its
// people: the write that leaves the switch on is plain whoever makes it, and the
// warning answers nobody what that write would not ask them to prove.
func (r *TenantDirectoryRules) DirectoryOnlyWarning(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, people ...DirectoryPeople) (*Warning, error) {
	on, found, err := r.switchState(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httpio.NewNotFoundMessagef("This %s does not exist.", r.word)
	}
	if on {
		return nil, httpio.NewConflictMessagef("This %[1]s is directory only already: only people who sign in through its directory enter it. There is nothing to turn on.", r.word)
	}
	directory, err := r.turnOn(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}

	caller := CallerFrom(ctx)
	count, counted, administratorsEnter := 0, false, false
	var named []DirectoryOutsider
	for _, auth := range people {
		administratorsEnter = administratorsEnter || auth.AdministratorsEnter
		if auth.Outsiders == nil {
			continue
		}
		counted = true
		outsiders, err := auth.Outsiders(ctx, txn, tenant)
		if err != nil {
			return nil, errors.Wrap(err, "resource.DirectoryOutsiders()")
		}
		count += len(outsiders)
		if len(outsiders) == 0 || caller.IsZero() {
			continue
		}
		lists, err := listsPeople(ctx, caller.Permissions, tenant, auth)
		if err != nil {
			return nil, err
		}
		if lists {
			named = append(named, outsiders...)
		}
	}
	slices.SortFunc(named, func(a, b DirectoryOutsider) int {
		if byName := strings.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username)); byName != 0 {
			return byName
		}

		return strings.Compare(a.UserID.String(), b.UserID.String())
	})
	var names []string
	for _, person := range named {
		names = append(names, outsiderName(person))
	}

	return &Warning{
		Consequence: directoryOnlySentence(r.directoryWords(directory), r.word, administratorsEnter, counted, count, len(names)),
		Names:       names,
		Confirm:     fmt.Sprintf("Admit only people who sign in through %s", r.directoryWords(directory)),
	}, nil
}

// outsiderName is a person as the warning lists them: by their name and what identifies
// them, or by what identifies them alone where the account holds no name.
func outsiderName(person DirectoryOutsider) string {
	if person.Name != nil && strings.TrimSpace(*person.Name) != "" {
		return fmt.Sprintf("%s (%s)", strings.TrimSpace(*person.Name), person.Username)
	}

	return person.Username
}

// directoryOnlySentence is the consequence of the switch in words: who will be able
// to enter, how many of the people seated in the tenant will not, and, where the
// warning lists fewer of them than it counts, that the list is the ones the caller may
// list, so a part of them is never read as all of them. counted says somebody read the
// people: where nobody did, the sentence gives no number, and does not say that nobody
// loses access. directory is the tenant's directory in the words a person knows it by
// (DirectoryWords). administratorsEnter says the tenant's own administrators keep their
// way in, and the sentence says it before it counts anyone: an administrator who has a
// password is not among the people who lose access, and the person about to close the
// tenant is told of the one other way into it. Where the switch then keeps nobody out,
// the sentence says that nobody loses access and not that everyone came through the
// directory, which such an administrator may never have done.
func directoryOnlySentence(directory, word string, administratorsEnter, counted bool, outsiders, named int) string {
	only := fmt.Sprintf("Only people who sign in through %s will be able to enter this %s.", directory, word)
	if administratorsEnter {
		only += fmt.Sprintf(" An administrator of this %s who has a password can still sign in with it and a code sent to their email.", word)
	}
	if !counted {
		return only + " Anyone seated here who signs in another way will lose access."
	}
	var lose string
	switch outsiders {
	case 0:
		if administratorsEnter {
			return only + " Nobody seated here will lose access."
		}

		return only + " Everyone seated here has signed in through it."
	case 1:
		lose = only + " 1 person seated here signs in another way and will lose access."
	default:
		lose = fmt.Sprintf("%s %d people seated here sign in another way and will lose access.", only, outsiders)
	}
	switch named {
	case 0, outsiders:
		return lose
	case 1:
		return lose + " The one of them you may list is named here."
	default:
		return fmt.Sprintf("%s The %d of them you may list are named here.", lose, named)
	}
}

// turnOn holds turning tenant's switch on to its rules and answers the tenant's
// directory: the tenant has one, the caller is signed in through it, and the caller
// has a password where their auth has a password method.
func (r *TenantDirectoryRules) turnOn(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (signin.Directory, error) {
	directory, has, err := r.directoryOf(ctx, txn, tenant)
	if err != nil {
		return signin.Directory{}, err
	}
	if !has {
		return signin.Directory{}, httpio.NewConflictMessagef("This %[1]s has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this %[1]s's directory, then turn directory only on.", r.word)
	}
	session := sessioninfo.DataFromCtx(ctx)
	established, ok := session.EstablishedBy()
	if !ok || (signin.Directory{Method: established.Method, Connection: established.Connection}) != directory {
		return signin.Directory{}, httpio.NewConflictMessagef("Directory only is turned on by a person who is signed in through this %[1]s's directory, %[2]s: that shows the directory works, and that you will not shut yourself out. %[3]s Sign in through %[2]s and try again.",
			r.word, r.directoryName(directory), establishedSentence(session, r.namers))
	}
	if err := r.requirePassword(ctx, txn); err != nil {
		return signin.Directory{}, err
	}

	return directory, nil
}

// requirePassword refuses a caller whose own account has no password, where an auth
// says the question applies to them (PasswordsBy): the caller's auth has a password
// method, and the caller is its person. Where none applies the rule stands down.
func (r *TenantDirectoryRules) requirePassword(ctx context.Context, txn ReadWriteTransaction) error {
	for _, read := range r.passwords {
		if read == nil {
			continue
		}
		has, applies, err := read(ctx, txn)
		if err != nil {
			return errors.Wrap(err, "resource.CallerPassword()")
		}
		if applies && !has {
			return httpio.NewConflictMessagef("Directory only is turned on by a person who has a password: an administrator of this %[1]s enters it with a password and a code sent to their email, and that is your way back in if the directory has a problem. Choose a password on Your account first, then try again.", r.word)
		}
	}

	return nil
}

// switchState reads tenant's directory-only flag as committed; found is false for a
// tenant the record does not hold. A record that carries no directory-only column has
// no switch, and every tenant's is off.
func (r *TenantDirectoryRules) switchState(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (on, found bool, err error) {
	if !r.columns.declared() {
		return false, false, errors.New("resource: a tenant's directory rules need the directories record's columns: build them with the generated New<Record>DirectoryRules")
	}
	if r.columns.DirectoryOnly == "" {
		return false, false, errors.New("resource: the tenant record declares no directory-only column (@directoryOnly), so it has no switch to turn")
	}
	statements, err := tenantDirectoryStatements(txn.DBType(), r.table, r.keyColumn, r.columns)
	if err != nil {
		return false, false, err
	}
	if txn.DBType() != SpannerDBType {
		return false, false, errTenantDirectoryUnsupportedDatabase
	}
	var rows []struct {
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}
	statement := spanner.Statement{SQL: statements.DirectoryOnly, Params: map[string]any{domainParamName: string(tenant)}}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, statement); err != nil {
		return false, false, errors.Wrap(err, "spxscan.Select()")
	}
	for _, row := range rows {
		return row.DirectoryOnly, true, nil
	}

	return false, false, nil
}

// directoryOf reads tenant's directory as committed; has is false for a tenant with
// none.
func (r *TenantDirectoryRules) directoryOf(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (directory signin.Directory, has bool, err error) {
	// No directory carries an empty method, so the read answers the tenant's own row
	// alone.
	held, err := r.held(ctx, txn, tenant, signin.Directory{})
	if err != nil {
		return signin.Directory{}, false, err
	}
	for _, row := range held {
		if accesstypes.Domain(row.Domain) == tenant {
			return signin.Directory{Method: sessioninfo.AuthMethod(row.Method), Connection: row.Connection}, true, nil
		}
	}

	return signin.Directory{}, false, nil
}

// turnsOn reports whether a write's value for the directory-only flag turns the switch
// on: the flag as a decoded patch carries it, a boolean or a pointer to one, of the
// field's own type or a type named over it. A write that carries no flag, and a nil
// pointer, turn nothing on. A value that is no boolean is taken as turning it on: the
// rule is asked, never skipped over a type this reading does not know.
func turnsOn(value any) bool {
	flag := reflect.ValueOf(value)
	for flag.IsValid() && (flag.Kind() == reflect.Pointer || flag.Kind() == reflect.Interface) {
		if flag.IsNil() {
			return false
		}
		flag = flag.Elem()
	}
	if !flag.IsValid() {
		return false
	}
	if flag.Kind() == reflect.Bool {
		return flag.Bool()
	}

	return true
}

// State answers tenant's directory-only switch as it stands, in the caller's
// transaction; a tenant the record does not hold is NotFound.
func (r *TenantDirectoryRules) State(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*DirectoryOnlyState, error) {
	on, found, err := r.switchState(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httpio.NewNotFoundMessagef("This %s does not exist.", r.word)
	}

	return &DirectoryOnlyState{DirectoryOnly: on}, nil
}

// DirectoryOnlyStateHandler serves a tenant's directory-only switch as it stands
// (DirectoryOnlyState, DirectoryOnlyRoute): what a tenant's Sign-in page shows the
// person who may turn it. The read is the tenant's own: it is served under the tenant's
// route and asked under the grants of the methods that turn the switch, Execute in the
// tenant's scope on one of methods (the one that turns it on, or the one that turns it
// off), where a caller with neither is Forbidden and learns nothing of the tenant. A
// grant that holds on a condition is refused the same way: a read has no request for
// the condition to judge. It writes nothing. client runs it in one transaction, rules
// are the tenant record's, and tenantParam is the route parameter that names the
// tenant. The generated handler of a tenant record that declares the switch builds it.
func DirectoryOnlyStateHandler(
	client Client, rules *TenantDirectoryRules, userPermissions func(r *http.Request) UserPermissions,
	tenantParam httpio.ParamType, methods ...accesstypes.Resource,
) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		tenant := httpio.Param[accesstypes.Domain](r, tenantParam)
		if err := holdsOneOf(ctx, userPermissions(r), tenant, methods); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		var state *DirectoryOnlyState
		if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			state, err = rules.State(ctx, txn, tenant)

			return err
		}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(state)
	})
}

// holdsOneOf holds the caller to the grant a tenant's read is asked under: Execute on
// one of methods, in the tenant's scope, on no condition. An answer that does not hold
// a method that was asked is no grant of it.
func holdsOneOf(ctx context.Context, perms UserPermissions, tenant accesstypes.Domain, methods []accesstypes.Resource) error {
	scope := accesstypes.DomainScope(tenant)
	decisions, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.Execute, methods...)
	if err != nil {
		return errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	for _, method := range methods {
		if decisions[method].IsGranted() {
			return nil
		}
	}

	return httpio.NewForbiddenMessagef("scope (%s), user (%s) does not have (%s) on %s", scope, perms.User(), accesstypes.Execute, methods)
}

// listsPeople reports whether the caller may list the people of one auth in tenant, as
// the list itself asks it: List on the tenant's list of them, and List on each field of
// it that names a person, on no condition. A field grant that holds on a condition
// lists some of the people and not others, so it names nobody here; and a declaration
// that names no list, or no field of it, names nobody either. An answer that does not
// hold a resource that was asked is no grant of it.
func listsPeople(ctx context.Context, perms UserPermissions, tenant accesstypes.Domain, people DirectoryPeople) (bool, error) {
	if people.List == "" || len(people.Fields) == 0 {
		return false, nil
	}
	scope := accesstypes.DomainScope(tenant)
	list, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.List, people.List)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if list[people.List].IsDenied() {
		return false, nil
	}
	asked := make([]accesstypes.Resource, 0, len(people.Fields))
	for _, field := range people.Fields {
		asked = append(asked, people.List.ResourceWithTag(field))
	}
	decisions, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.List, asked...)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	for _, field := range asked {
		if !decisions[field].IsGranted() {
			return false, nil
		}
	}

	return true, nil
}
