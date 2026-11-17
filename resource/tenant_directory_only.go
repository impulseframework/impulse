package resource

import (
	"context"
	"fmt"
	"net/http"
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

// DirectoryOnlyRoute is the route of the read that says what turning a tenant's
// directory-only switch on would do (DirectoryOnlyHandler), under an outlet's prefix:
// GET <prefix>/directory-only?domain=<tenant>. The generated router registers it on
// every outlet that serves sessions and the tenant record, for a record that declares
// the switch.
const DirectoryOnlyRoute = "directory-only"

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
	// People are the people seated in the tenant who will lose access, by username:
	// everyone with a seat and no sign-in through the tenant's directory, in every auth
	// whose people the tenant seats. Never nil.
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
// Nobody is exempt, a super administrator included: the rule is about the directory,
// not about who may enter. The generated update of the tenant record asks it once the
// caller's grants have answered (PatchSet.Permitted), for a record that declares the
// switch (@directoryOnly) and a directories record.
func (r *TenantDirectoryRules) DirectoryOnly(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, value any) error {
	if !turnsOn(value) {
		return nil
	}
	on, _, err := r.switchState(ctx, txn, tenant)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	_, err = r.turnOn(ctx, txn, tenant)

	return err
}

// Consequence answers what turning tenant's directory-only switch on would do, in the
// caller's transaction, and writes nothing: the tenant's directory, the sentence a
// confirmation shows, and the people it names, read through outsiders, one reader for
// each auth whose people the tenant seats. It refuses as DirectoryOnly refuses, with
// the same sentences, so a page that asks first shows the refusal instead of a
// confirmation; a tenant the record does not hold is NotFound. A tenant that is
// directory only already is answered as it stands: the people its switch keeps out now.
func (r *TenantDirectoryRules) Consequence(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, outsiders ...DirectoryOutsiders) (*DirectoryOnlyConsequence, error) {
	on, found, err := r.switchState(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httpio.NewNotFoundMessagef("This %s does not exist.", r.word)
	}
	var directory signin.Directory
	if on {
		directory, _, err = r.directoryOf(ctx, txn, tenant)
	} else {
		directory, err = r.turnOn(ctx, txn, tenant)
	}
	if err != nil {
		return nil, err
	}

	people := []DirectoryOutsider{}
	for _, read := range outsiders {
		if read == nil {
			continue
		}
		found, err := read(ctx, txn, tenant)
		if err != nil {
			return nil, errors.Wrap(err, "resource.DirectoryOutsiders()")
		}
		people = append(people, found...)
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
		Sentence:   directoryOnlySentence(directory, r.word, len(people)),
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

// turnsOn reports whether a write's value for the directory-only flag is true: the
// flag as a decoded patch carries it.
func turnsOn(value any) bool {
	switch on := value.(type) {
	case bool:
		return on
	case *bool:
		return on != nil && *on
	default:
		return false
	}
}

// DirectoryOnlyHandler serves the read a tenant's Sign-in page calls before it saves
// the directory-only switch (DirectoryOnlyRoute): what turning the switch on would do
// to the tenant the domain query parameter names (DirectoryOnlyConsequence), or the
// refusal the write itself would answer, in the same words. It is asked under the
// grant the write is: Update on the tenant record and on its directory-only field, in
// the global scope, where a caller without it is Forbidden and learns nothing of the
// tenant. Nothing is written.
//
// resourceName is the tenant record's resource and field the directory-only field as
// grants name it; client runs the read in one transaction, and outsiders are the
// readers of the people the switch keeps out, one for each auth whose people a tenant
// seats. The generated handler of a tenant record that declares the switch supplies
// all of them.
func DirectoryOnlyHandler(
	client Client, rules *TenantDirectoryRules, userPermissions func(r *http.Request) UserPermissions,
	resourceName accesstypes.Resource, field accesstypes.Tag, outsiders ...DirectoryOutsiders,
) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		tenant := accesstypes.Domain(r.URL.Query().Get("domain"))
		if tenant == "" {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage("The domain query parameter names the tenant: directory-only?domain=<tenant>."))
		}
		perms := userPermissions(r)
		decisions, err := perms.Check(ctx, RequestEnvironment(), accesstypes.GlobalScope(), accesstypes.Update, resourceName, resourceName.ResourceWithTag(field))
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "resource.UserPermissions.Check()"))
		}
		if denied := decisions.DeniedResources(); len(denied) > 0 {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewForbiddenMessagef("scope (%s), user (%s) does not have (%s) on %s", accesstypes.GlobalScope(), perms.User(), accesstypes.Update, denied))
		}

		var consequence *DirectoryOnlyConsequence
		if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			consequence, err = rules.Consequence(ctx, txn, tenant, outsiders...)

			return err
		}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(consequence)
	})
}
