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
// stands (DirectoryOnlyStateHandler), under the tenant's own route:
// GET <prefix>/<tenants>/{tenant}/directory-only. The generated router registers it,
// the read of what turning the switch on would do (DirectoryOnlyConsequenceRoute) and
// the method that turns it (Set<Record>DirectoryOnly) on every outlet that serves
// sessions and the tenant record, for a record that declares the switch.
const DirectoryOnlyRoute = "directory-only"

// DirectoryOnlyConsequenceRoute is the route of the read that says what turning a
// tenant's directory-only switch on would do (DirectoryOnlyConsequenceHandler), under
// the tenant's own route: GET <prefix>/<tenants>/{tenant}/directory-only/consequence.
const DirectoryOnlyConsequenceRoute = DirectoryOnlyRoute + "/consequence"

// SetDirectoryOnlyRequest is the body of the method that turns a tenant's
// directory-only switch on or off (Set<Record>DirectoryOnly, a method in the tenant's
// own scope, which the generator writes for a tenant record that declares the switch).
// The body says which: a request that names neither is refused.
type SetDirectoryOnlyRequest struct {
	DirectoryOnly *bool `json:"directoryOnly"`
}

// Turn answers which way the request turns the switch, or the refusal of a body that
// does not say.
func (r *SetDirectoryOnlyRequest) Turn() (on bool, err error) {
	if r == nil || r.DirectoryOnly == nil {
		return false, httpio.NewBadRequestMessage(`Say which way to turn directory only: {"directoryOnly": true} turns it on, {"directoryOnly": false} turns it off.`)
	}

	return *r.DirectoryOnly, nil
}

// DirectoryOnlyState is a tenant's directory-only switch as it stands.
type DirectoryOnlyState struct {
	DirectoryOnly bool `json:"directoryOnly"`
}

// DirectoryOutsider is a person who holds a seat in a tenant and has never signed in
// through the tenant's directory: someone the tenant's directory-only switch keeps
// out for as long as it is on.
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
// settings shut them out. The generated identity client of each auth whose people a
// tenant seats supplies it (DirectoryOutsiders), over the tenant's own list of its
// people; an application names none of them.
type DirectoryOutsiders func(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) ([]DirectoryOutsider, error)

// DirectoryPeople is one auth's part of what turning a tenant's directory-only switch
// on would do: the reader of the people it would keep out, and the tenant's list of
// that auth's people, which decides whether the caller is told who they are. The read
// names people only to someone who may list them: a caller who holds List, in the
// tenant, on the list and, on no condition, on each field of it that names a person.
// Anyone else is told how many there are and nobody's name. A reader with no list, or
// with no field named, names nobody.
type DirectoryPeople struct {
	// List is the tenant's list of the auth's people as grants name it, and Fields the
	// fields of it that name a person: the ones the read answers (DirectoryOutsider).
	List   accesstypes.Resource
	Fields []accesstypes.Tag
	// Outsiders reads the people the switch would keep out.
	Outsiders DirectoryOutsiders
}

// DirectoryOnlyConsequence is what turning a tenant's directory-only switch on would
// do, in words the server writes, for a page to show before the switch is saved.
type DirectoryOnlyConsequence struct {
	// Directory is the tenant's directory as a sentence names it ("Google Workspace
	// acme.example"), and Connection the name its people know it by ("acme.example").
	Directory  string `json:"directory"`
	Connection string `json:"connection"`
	// Sentence says who will be able to enter the tenant and how many of the people
	// seated in it will not: "Only people who sign in through acme.example will be able
	// to enter this tenant. 4 people seated here sign in another way and will lose
	// access."
	Sentence string `json:"sentence"`
	// Count is how many people seated in the tenant will lose access: everyone with a
	// seat and no sign-in through the tenant's directory, in every auth whose people the
	// tenant seats.
	Count int `json:"count"`
	// People are those people by username, for a caller who may list them: the read
	// names the people of an auth only to a caller who holds the tenant's list of that
	// auth's people (DirectoryPeople), so it holds fewer people than Count says, or
	// none, for a caller who does not. Never nil.
	People []DirectoryOutsider `json:"people"`
	// Confirm is what the button that turns the switch on says it does: "Admit only
	// people who sign in through acme.example".
	Confirm string `json:"confirm"`
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
//     sign-in is not shut out by it.
//
// The rules read what is committed, and a transaction's reads do not see what it has
// buffered, so the transaction notes each write the rules pass (directoryWrites): a
// request that removes the tenant's directory and turns the switch on is refused,
// whichever comes first, as each would be after the other had committed.
//
// Nobody is exempt, a super administrator included: the rule is about the directory,
// not about who may enter. The switch is the tenant's own, turned by the tenant's
// administrator: the generated method of a tenant record that declares it
// (@directoryOnly) and a directories record, Set<Record>DirectoryOnly in the tenant's
// scope, asks the rule once the caller's Execute grant has answered and then writes the
// flag. No other request writes it: the record's field is output only on the wire, so
// neither the record's update nor its create carries it.
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

// Consequence answers what turning tenant's directory-only switch on would do, in the
// caller's transaction, and writes nothing: the tenant's directory, the sentence a
// confirmation shows, and the people it names, read through outsiders, one reader for
// each auth whose people the tenant seats. It refuses as DirectoryOnly refuses, with
// the same sentences, so a page that asks first shows the refusal instead of a
// confirmation; a tenant the record does not hold is NotFound. A tenant that is
// directory only already has nothing to turn on, and the read is refused as a Conflict
// that says so and names neither its directory nor its people: the write that leaves the
// switch on is plain whoever makes it, and the read answers nobody what that write
// would not ask them to prove. Every person is named: the served read names people
// only to a caller who may list them (DirectoryOnlyConsequenceHandler).
func (r *TenantDirectoryRules) Consequence(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, outsiders ...DirectoryOutsiders) (*DirectoryOnlyConsequence, error) {
	readers := make([]outsidersReading, 0, len(outsiders))
	for _, read := range outsiders {
		readers = append(readers, outsidersReading{read: read, named: true})
	}

	return r.consequence(ctx, txn, tenant, readers)
}

// outsidersReading is one auth's reader of the people the switch would keep out, and
// whether the answer names them or only counts them.
type outsidersReading struct {
	read  DirectoryOutsiders
	named bool
}

// consequence is Consequence over readers that each say whether their people are
// named: everyone is counted, and the people of a reader that does not name them are
// left out of the answer's list.
func (r *TenantDirectoryRules) consequence(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, readers []outsidersReading) (*DirectoryOnlyConsequence, error) {
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

	count := 0
	people := []DirectoryOutsider{}
	for _, reader := range readers {
		if reader.read == nil {
			continue
		}
		found, err := reader.read(ctx, txn, tenant)
		if err != nil {
			return nil, errors.Wrap(err, "resource.DirectoryOutsiders()")
		}
		count += len(found)
		if reader.named {
			people = append(people, found...)
		}
	}
	slices.SortFunc(people, func(a, b DirectoryOutsider) int {
		if byName := strings.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username)); byName != 0 {
			return byName
		}

		return strings.Compare(a.UserID.String(), b.UserID.String())
	})

	return &DirectoryOnlyConsequence{
		Directory:  DirectoryName(directory),
		Connection: directory.Connection,
		Sentence:   directoryOnlySentence(directory, r.word, count),
		Count:      count,
		People:     people,
		Confirm:    fmt.Sprintf("Admit only people who sign in through %s", directory.Connection),
	}, nil
}

// directoryOnlySentence is the consequence of the switch in words: who will be able
// to enter, and how many of the people seated in the tenant will not.
func directoryOnlySentence(directory signin.Directory, word string, outsiders int) string {
	only := fmt.Sprintf("Only people who sign in through %s will be able to enter this %s.", directory.Connection, word)
	switch outsiders {
	case 0:
		return only + " Everyone seated here has signed in through it."
	case 1:
		return only + " 1 person seated here signs in another way and will lose access."
	default:
		return fmt.Sprintf("%s %d people seated here sign in another way and will lose access.", only, outsiders)
	}
}

// turnOn holds turning tenant's switch on to its rules and answers the tenant's
// directory: the tenant has one, and the caller is signed in through it.
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
		return signin.Directory{}, httpio.NewConflictMessagef("Directory only is turned on by a person who is signed in through this %[1]s's directory, %[2]s: that shows the directory works, and that you will not shut yourself out. %[3]s Sign in through %[2]s and try again.", r.word, DirectoryName(directory), establishedSentence(session))
	}

	return directory, nil
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

// DirectoryOnlyReads is what the two reads of a tenant's directory-only switch are
// served from: the switch as it stands (State, DirectoryOnlyRoute) and what turning it
// on would do (Consequence, DirectoryOnlyConsequenceRoute). Both are the tenant's own:
// each is served under the tenant's route and asked under the grant the method that
// turns the switch is, Execute on it in the tenant's scope, where a caller without it
// is Forbidden and learns nothing of the tenant. A grant that holds on a condition is
// refused the same way: a read has no request for the condition to judge. Neither
// writes anything. The generated handlers of a tenant record that declares the switch
// build one (NewDirectoryOnlyReads).
type DirectoryOnlyReads struct {
	client          Client
	rules           *TenantDirectoryRules
	userPermissions func(r *http.Request) UserPermissions
	method          accesstypes.Resource
	tenantParam     httpio.ParamType
	people          []DirectoryPeople
}

// NewDirectoryOnlyReads builds the reads of a tenant's directory-only switch: client
// runs each in one transaction, rules are the tenant record's, method is the method
// that turns the switch as grants name it, tenantParam the route parameter that names
// the tenant, and people one entry for each auth whose people a tenant seats.
func NewDirectoryOnlyReads(
	client Client, rules *TenantDirectoryRules, userPermissions func(r *http.Request) UserPermissions,
	method accesstypes.Resource, tenantParam httpio.ParamType, people ...DirectoryPeople,
) *DirectoryOnlyReads {
	return &DirectoryOnlyReads{client: client, rules: rules, userPermissions: userPermissions, method: method, tenantParam: tenantParam, people: people}
}

// State serves the switch as it stands (DirectoryOnlyState): what a tenant's Sign-in
// page shows the person who may turn it.
func (s *DirectoryOnlyReads) State() http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		tenant, _, err := s.admit(ctx, r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		var state *DirectoryOnlyState
		if err := s.client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			state, err = s.rules.State(ctx, txn, tenant)

			return err
		}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(state)
	})
}

// Consequence serves the read a tenant's Sign-in page calls before it turns the switch
// on: what turning it on would do to the tenant (DirectoryOnlyConsequence), or the
// refusal the method itself would answer, in the same words. Everyone the switch would
// keep out is counted. A person is named only to a caller who may list them: the people
// of an auth are named where the caller holds the tenant's list of that auth's people
// (DirectoryPeople), and counted alone where the caller does not.
func (s *DirectoryOnlyReads) Consequence() http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		tenant, perms, err := s.admit(ctx, r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		readers := make([]outsidersReading, 0, len(s.people))
		for _, people := range s.people {
			named, err := listsPeople(ctx, perms, tenant, people)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			readers = append(readers, outsidersReading{read: people.Outsiders, named: named})
		}

		var consequence *DirectoryOnlyConsequence
		if err := s.client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			consequence, err = s.rules.consequence(ctx, txn, tenant, readers)

			return err
		}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(consequence)
	})
}

// admit names the tenant the request's route carries and holds the caller to the grant
// the reads are asked under: Execute on the method that turns the switch, in the
// tenant's scope, on no condition.
func (s *DirectoryOnlyReads) admit(ctx context.Context, r *http.Request) (accesstypes.Domain, UserPermissions, error) {
	tenant := httpio.Param[accesstypes.Domain](r, s.tenantParam)
	scope := accesstypes.DomainScope(tenant)
	perms := s.userPermissions(r)
	decisions, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.Execute, s.method)
	if err != nil {
		return "", nil, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if !decisions[s.method].IsGranted() {
		return "", nil, httpio.NewForbiddenMessagef("scope (%s), user (%s) does not have (%s) on %s", scope, perms.User(), accesstypes.Execute, []accesstypes.Resource{s.method})
	}

	return tenant, perms, nil
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
