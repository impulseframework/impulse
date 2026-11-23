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
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/tracer"
)

// TenantSignInSettingsRoute is the route of the read of a tenant's sign-in settings as
// they stand (TenantSignInSettings), under the tenant's own route:
// GET <prefix>/<tenants>/{tenant}/sign-in-settings. The generated router registers it on
// every outlet that serves sessions and one of the tenant's own sign-in methods, for a
// tenant record that declares a directories record.
const TenantSignInSettingsRoute = "sign-in-settings"

// DirectoryOnlyWord is the word a person types before a tenant is made directory only
// (the warning of Make<Record>DirectoryOnly): the tenant is closed to every sign-in but
// one through its directory. A warning's word is one lowercase word, which a
// directory's name is not, so the word is the same for every tenant.
const DirectoryOnlyWord = "close"

// TenantSignInSettings is how a tenant's people sign in, as it stands, said for the
// caller: the tenant's directory, the role its people get, whether it is directory only
// and whom that keeps out, whether the tenant vouches for its directory's second factor,
// and what each change takes of the caller.
type TenantSignInSettings struct {
	DirectoryOnly bool `json:"directoryOnly"`
	// Directory is the tenant's directory as people know it (DirectoryName), on or off,
	// and never its connection. It is empty for a tenant with no directory.
	Directory string `json:"directory,omitempty"`
	// DefaultRole is the role a person gets the first time they sign in through the
	// tenant's directory, in words (words.RoleInWords). It is empty where the tenant
	// admits the directory's people by invitation only, has no directory, or its
	// directories record declares no default seat.
	DefaultRole string `json:"defaultRole,omitempty"`
	// Passwords says the caller's auth has a password method (PasswordsBy), so that
	// turning the switch on asks the caller for a password, and an administrator of the
	// tenant who has one still enters it while it is on.
	Passwords bool `json:"passwords,omitempty"`
	// SignedIn says, to the caller, how their session was signed in, in the words a
	// tenant's refusals say it in.
	SignedIn string `json:"signedIn"`
	// SessionDirectory is the organization's directory the caller's session was signed in
	// through, as people know it (DirectoryName), whichever tenant's it is. It is empty
	// for a session that came through no organization's directory.
	SessionDirectory string `json:"sessionDirectory,omitempty"`
	// People counts the tenant's people and the ones the switch keeps out (CountedBy),
	// on or off. It is absent for a tenant with no directory, for rules that count
	// nobody, and for a tenant record that declares no switch.
	People *DirectoryOnlyCount `json:"people,omitempty"`
	// Conditions are what turning the switch on takes of the person who turns it, said
	// for the caller in the order the call checks them: whether the caller meets each,
	// and what they do where they do not. A page shows them before the person tries.
	// There are none while the switch is on, none for a tenant with no directory, which
	// is refused before anything is asked of the person, and none for a tenant record
	// that declares no switch.
	Conditions []TenantSignInCondition `json:"conditions,omitempty"`
	// SecondFactor is the second factor's part (DirectorySecondFactor), said for a tenant
	// that has a directory where the directories record carries the second-factor
	// column. It is absent otherwise.
	SecondFactor *DirectorySecondFactor `json:"secondFactor,omitempty"`
}

// DirectoryOnlyCount is how many people hold a role in a tenant, Seated, and how many
// of them the tenant's directory-only switch keeps out, Outside: the people who have
// never signed in through the tenant's directory, counted as the warning that turns the
// switch on counts them (DirectoryOutsiders), so the two always say the same number.
type DirectoryOnlyCount struct {
	Seated  int `json:"seated"`
	Outside int `json:"outside"`
	// Shares says the count auth by auth, one entry for each auth whose people the rules
	// count, in the order the rules were given them. The totals above are their sums. A
	// page that links the count to one auth's people reads that auth's share, so a link
	// never says more people than it opens.
	Shares []DirectoryOnlyShare `json:"shares,omitempty"`
}

// DirectoryOnlyShare is one auth's part of a tenant's count: how many of that auth's
// people the tenant seats, and how many of them the switch keeps out. Label is what a
// page calls the auth's people, and People the tenant's list of them as grants name it,
// which a page opens to show who they are.
type DirectoryOnlyShare struct {
	Label   string               `json:"label"`
	People  accesstypes.Resource `json:"people"`
	Seated  int                  `json:"seated"`
	Outside int                  `json:"outside"`
}

// TenantSignInCondition is one thing a change of a tenant's sign-in settings takes of
// the person who makes it, said for one caller. The call's refusal of a caller who does
// not meet it is Sentence, a space, and ToDo, so the page that shows a condition and the
// call that refuses on it say the same words.
type TenantSignInCondition struct {
	// Sentence says the condition and why it is asked.
	Sentence string `json:"sentence"`
	// Met says whether the caller meets it.
	Met bool `json:"met"`
	// The field ToDo says what the caller does to meet it. It is empty where they
	// meet it.
	ToDo string `json:"toDo,omitempty"`
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

// DirectoryAdministrator is one of a tenant's own administrators who has a password: a
// person who still enters the tenant while it is directory only, with the password and a
// code mailed to them (signin.TenantDeclaration.AdmitsAdministrator), and whom the
// warning that turns the switch on names, so the person closing the tenant sees who
// keeps that way in.
type DirectoryAdministrator struct {
	// UserID is the id of the person's account, and Username what identifies them.
	UserID   impulse.UUID `json:"userId"`
	Username string       `json:"username"`
	// Name is the person's name as people read it, absent where the account holds none.
	Name *string `json:"name,omitempty"`
}

// DirectoryAdministrators reads the people of one auth who administer tenant and have a
// password, in the caller's transaction: who holds one of the application's
// tenant-administrator roles in the tenant itself, of their own or through a group kept
// in the tenant, inside its period, with a password on the account. They are the people
// DirectoryOutsiders leaves out for the same reason, so the two never disagree. A
// disabled account is read like any other, and so is a super administrator who holds
// such a role in the tenant itself: nobody is read for being a super administrator
// alone. The generated identity client of each auth that has a password method and
// names the roles that administer a tenant supplies it (DirectoryAdministrators), over
// the tenant's own list of its people.
type DirectoryAdministrators func(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) ([]DirectoryAdministrator, error)

// DirectoryInvitation is an invitation into a tenant that has not been accepted and gives
// one of the tenant's administrator roles: whoever accepts it can choose a password and
// enter the tenant without its directory, as its administrators who have one do.
type DirectoryInvitation struct {
	// ID is the invitation's id, and Email the address it was sent to.
	ID    impulse.UUID `json:"id"`
	Email string       `json:"email"`
	// Name is the invited person's name as the invitation holds it, absent where it
	// holds none.
	Name *string `json:"name,omitempty"`
}

// DirectoryInvitations reads one auth's invitations into tenant that stand (not
// accepted, withdrawn or expired) and give one of the application's tenant-administrator
// roles, in the caller's transaction. The generated identity client of each auth that
// has a password method and lists its invitations supplies it (DirectoryInvitations),
// over the tenant's own list of its invitations.
type DirectoryInvitations func(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) ([]DirectoryInvitation, error)

// DirectoryPeople is one auth's part of what turning a tenant's directory-only switch
// on would do: the reader of the people it would keep out, the readers of who keeps the
// administrators' way in, and the tenant's lists of that auth's people and invitations,
// which decide whether the caller is told who they are. The warning names people only
// to someone who may list them: a caller who holds List, in the tenant, on the list and,
// on no condition, on each field of it that names a person. Anyone else is told how
// many there are and nobody's name. A reader with no list, or with no field named,
// names nobody.
type DirectoryPeople struct {
	// Label is what a page calls the auth's people ("Crew"), which the read of the
	// switch says the auth's share of its count under (DirectoryOnlyShare).
	Label string
	// List is the tenant's list of the auth's people as grants name it, and Fields the
	// fields of it that name a person: the ones the warning lists a person by
	// (DirectoryOutsider, DirectoryAdministrator).
	List   accesstypes.Resource
	Fields []accesstypes.Tag
	// Seated counts the auth's people the tenant seats, for the read of the switch; a
	// nil reader counts nobody.
	Seated SeatedCount
	// Outsiders reads the people the switch would keep out.
	Outsiders DirectoryOutsiders
	// AdministratorsEnter answers whether the auth's people keep the administrators' way
	// in, asked at each warning: the auth has a password method and names the roles that
	// administer a tenant, so the tenant's own administrator who has a password still
	// enters, with a code mailed to them. The warning says so, since it is the one other
	// way into the tenant, and reads who has it through the two readers below; where it
	// answers false or is nil, neither is called.
	AdministratorsEnter func() bool
	// Administrators reads the tenant's own administrators among the auth's people who
	// have a password: the warning counts them in a note of its own, and names them to a
	// caller who may list the people (List and Fields).
	Administrators DirectoryAdministrators
	// InvitationList is the tenant's list of the auth's invitations as grants name it,
	// and InvitationFields the fields of it that name the person invited: an invitation
	// is named only to a caller who holds List, in the tenant, on the list and, on no
	// condition, on each of them.
	InvitationList   accesstypes.Resource
	InvitationFields []accesstypes.Tag
	// Invitations reads the invitations into the tenant that stand and give one of its
	// administrator roles: the warning counts them in a note of its own, after the
	// administrators', and names them to a caller who may list them.
	Invitations DirectoryInvitations
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
// The read of the tenant's sign-in settings (SignInSettings) says the last two for the
// caller before they try, in the words these refusals use: one place holds the
// sentences, and a page shows the read's and holds none of its own.
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
// not, counted through the rules' people (CountedBy), one entry for each auth whose
// people the tenant seats; the confirm words say what the button does. Where no entry
// reads anybody (an application whose people no generated management lists) nobody was
// counted, and the sentence says who will lose access without a number: it never says
// that everyone has signed in through the directory on a count nobody made.
//
// Everyone the switch would keep out is counted. A person is named only to a caller who
// may list them: the people of an auth are named where the request's caller
// (CallerFrom) holds the tenant's list of that auth's people (DirectoryPeople), and
// counted alone where the caller does not. A call outside a request names nobody.
//
// Where an auth's people keep the administrators' way in (AdministratorsEnter), the
// warning also says who has it, in notes under the consequence (Also): first the
// tenant's own administrators who have a password, then the invitations into the tenant
// not yet accepted that give one of its administrator roles, each counted and named as
// the people the switch keeps out are, an invitation by the tenant's list of the auth's
// invitations. A note with nobody to count is left out.
//
// It refuses as DirectoryOnly refuses, with the same sentences, so a page that asks
// first shows the refusal instead of a dialog; a tenant the record does not hold is
// NotFound. A tenant that is directory only already has nothing to turn on, and the
// warning is refused as a Conflict that says so and names neither its directory nor its
// people: the write that leaves the switch on is plain whoever makes it, and the
// warning answers nobody what that write would not ask them to prove.
func (r *TenantDirectoryRules) DirectoryOnlyWarning(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*Warning, error) {
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
	var wayIn directoryWayIn
	for i := range r.people {
		auth := &r.people[i]
		enter := auth.AdministratorsEnter != nil && auth.AdministratorsEnter()
		administratorsEnter = administratorsEnter || enter
		var outsiders []DirectoryOutsider
		if auth.Outsiders != nil {
			counted = true
			outsiders, err = auth.Outsiders(ctx, txn, tenant)
			if err != nil {
				return nil, errors.Wrap(err, "resource.DirectoryOutsiders()")
			}
			count += len(outsiders)
		}
		administrators, invitations, err := readWayIn(ctx, txn, tenant, auth, enter)
		if err != nil {
			return nil, err
		}
		wayIn.administrators += len(administrators)
		wayIn.invitations += len(invitations)
		// One answer for the entry's people names both the people the switch keeps out
		// and the administrators who keep their way in.
		lists, err := namesTo(ctx, caller, tenant, auth.List, auth.Fields, len(outsiders)+len(administrators))
		if err != nil {
			return nil, err
		}
		if lists {
			named = append(named, outsiders...)
			wayIn.namedAdministrators = append(wayIn.namedAdministrators, administrators...)
		}
		lists, err = namesTo(ctx, caller, tenant, auth.InvitationList, auth.InvitationFields, len(invitations))
		if err != nil {
			return nil, err
		}
		if lists {
			wayIn.namedInvitations = append(wayIn.namedInvitations, invitations...)
		}
	}
	slices.SortFunc(named, func(a, b DirectoryOutsider) int {
		return listedOrder(a.Username, b.Username, a.UserID, b.UserID)
	})
	var names []string
	for _, person := range named {
		names = append(names, listedName(person.Name, person.Username))
	}

	return &Warning{
		Consequence: directoryOnlySentence(r.directoryWords(directory), r.word, administratorsEnter, counted, count, len(names)),
		Names:       names,
		Confirm:     fmt.Sprintf("Admit only people who sign in through %s", r.directoryWords(directory)),
		Also:        wayIn.notes(r.word),
	}, nil
}

// directoryWayIn is who keeps a way into a tenant past its directory, as the
// directory-only warning counts them across every auth's entry: the tenant's own
// administrators who have a password, and the invitations not yet accepted that would
// make another, with the ones the caller may be told of.
type directoryWayIn struct {
	administrators      int
	invitations         int
	namedAdministrators []DirectoryAdministrator
	namedInvitations    []DirectoryInvitation
}

// notes are the warning's notes on who keeps the way in, in the tenant word: first the
// administrators', then the invitations', each left out where it counts nobody, and nil
// where both are. A note's names are sorted as the people the switch keeps out are.
func (w *directoryWayIn) notes(word string) []WarningNote {
	var notes []WarningNote
	if w.administrators > 0 {
		slices.SortFunc(w.namedAdministrators, func(a, b DirectoryAdministrator) int {
			return listedOrder(a.Username, b.Username, a.UserID, b.UserID)
		})
		names := listedNames(w.namedAdministrators, func(person DirectoryAdministrator) string {
			return listedName(person.Name, person.Username)
		})
		notes = append(notes, WarningNote{Says: directoryAdministratorsSentence(word, w.administrators, len(names)), Names: names})
	}
	if w.invitations > 0 {
		slices.SortFunc(w.namedInvitations, func(a, b DirectoryInvitation) int {
			return listedOrder(a.Email, b.Email, a.ID, b.ID)
		})
		names := listedNames(w.namedInvitations, func(invitation DirectoryInvitation) string {
			return listedName(invitation.Name, invitation.Email)
		})
		notes = append(notes, WarningNote{Says: directoryInvitationsSentence(word, w.invitations, len(names)), Names: names})
	}

	return notes
}

// listedNames are the people a note names, one a line, in the order given, each as name
// writes them: nil where there is nobody to name, so a note that names nobody carries no
// list.
func listedNames[T any](people []T, name func(T) string) []string {
	if len(people) == 0 {
		return nil
	}
	names := make([]string, 0, len(people))
	for _, person := range people {
		names = append(names, name(person))
	}

	return names
}

// readWayIn reads one auth's part of who keeps a way into tenant past its directory: the
// tenant's own administrators who have a password and the invitations that would make
// another, each where the entry supplies its reader. An entry whose people keep no
// administrators' way in (enter, AdministratorsEnter's answer at this warning) has
// neither reader called.
func readWayIn(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, auth *DirectoryPeople, enter bool) ([]DirectoryAdministrator, []DirectoryInvitation, error) {
	if !enter {
		return nil, nil, nil
	}
	var administrators []DirectoryAdministrator
	if auth.Administrators != nil {
		var err error
		administrators, err = auth.Administrators(ctx, txn, tenant)
		if err != nil {
			return nil, nil, errors.Wrap(err, "resource.DirectoryAdministrators()")
		}
	}
	var invitations []DirectoryInvitation
	if auth.Invitations != nil {
		var err error
		invitations, err = auth.Invitations(ctx, txn, tenant)
		if err != nil {
			return nil, nil, errors.Wrap(err, "resource.DirectoryInvitations()")
		}
	}

	return administrators, invitations, nil
}

// namesTo reports whether the warning names to the request's caller what it counted on
// one of the tenant's lists: there is somebody counted, the call has a caller, and the
// caller may list them (listsPeople). Nothing is asked where nobody was counted or the
// call is outside a request.
func namesTo(ctx context.Context, caller *Caller, tenant accesstypes.Domain, list accesstypes.Resource, fields []accesstypes.Tag, counted int) (bool, error) {
	if counted == 0 || caller.IsZero() {
		return false, nil
	}

	return listsPeople(ctx, caller.Permissions, tenant, list, fields)
}

// listedOrder orders people as the warning lists them: by what identifies them (a
// username, or an invitation's address) without case, then by id, so two who read alike
// keep one order.
func listedOrder(a, b string, aID, bID impulse.UUID) int {
	if byName := strings.Compare(strings.ToLower(a), strings.ToLower(b)); byName != 0 {
		return byName
	}

	return strings.Compare(aID.String(), bID.String())
}

// listedName is a person as the warning lists them: by their name and what identifies
// them (a username, or the address an invitation was sent to), or by what identifies
// them alone where no name is held.
func listedName(name *string, identifies string) string {
	if name != nil && strings.TrimSpace(*name) != "" {
		return fmt.Sprintf("%s (%s)", strings.TrimSpace(*name), identifies)
	}

	return identifies
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

// directoryAdministratorsSentence is the warning's note on the tenant's own
// administrators who have a password, in the tenant word: how many can still enter
// without the directory, and to take the role away first from one who should not, since
// the switch changes nobody's way in. Where the note names fewer of them than it counts
// and at least one, it says the names are the ones the caller may list (namedEnding).
func directoryAdministratorsSentence(word string, administrators, named int) string {
	says := fmt.Sprintf("%d administrators of this %s have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in.", administrators, word)
	if administrators == 1 {
		says = fmt.Sprintf("1 administrator of this %s has a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in.", word)
	}

	return says + namedEnding(administrators, named)
}

// directoryInvitationsSentence is the warning's note on the invitations into the tenant
// not yet accepted that give a role that administers it, in the tenant word: how many
// stand, that whoever accepts one can choose a password and enter without the directory,
// and to withdraw first one that should not stand. Where the note names fewer of them
// than it counts and at least one, it says the names are the ones the caller may list
// (namedEnding).
func directoryInvitationsSentence(word string, invitations, named int) string {
	says := fmt.Sprintf("%d invitations that have not been accepted give a role that administers this %s. Whoever accepts one can choose a password and enter without the directory. Withdraw first any that should not stand.", invitations, word)
	if invitations == 1 {
		says = fmt.Sprintf("1 invitation that has not been accepted gives a role that administers this %s. Whoever accepts it can choose a password and enter without the directory. Withdraw it first if it should not stand.", word)
	}

	return says + namedEnding(invitations, named)
}

// namedEnding is what a note of the warning adds after its sentence where it names fewer
// than it counts and at least one: that the names are the ones the caller may list, so a
// part of them is never read as all of them. Where the note names nobody, or everyone it
// counts, it adds nothing.
func namedEnding(counted, named int) string {
	switch named {
	case 0, counted:
		return ""
	case 1:
		return " The one of them you may list is named here."
	default:
		return fmt.Sprintf(" The %d of them you may list are named here.", named)
	}
}

// turnOn holds turning tenant's switch on to its rules and answers the tenant's
// directory: the tenant has one, the caller is signed in through it, and the caller
// has a password where their auth has a password method. A caller who does not meet
// one of the two things asked of them is refused in the words the read of the tenant's
// sign-in settings says it in (TenantSignInCondition), so a page that shows them and
// this refusal never differ.
func (r *TenantDirectoryRules) turnOn(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (signin.Directory, error) {
	directory, has, err := r.directoryOf(ctx, txn, tenant)
	if err != nil {
		return signin.Directory{}, err
	}
	if !has {
		return signin.Directory{}, httpio.NewConflictMessagef("This %[1]s has no directory, so it cannot be directory only: nobody could enter it. Set its directory first: sign in through your organization's directory, make it this %[1]s's directory, then turn directory only on.", r.word)
	}
	if through := r.throughDirectory(ctx, directory); !through.Met {
		return signin.Directory{}, through.refusal()
	}
	password, applies, err := r.hasPassword(ctx, txn)
	if err != nil {
		return signin.Directory{}, err
	}
	if applies && !password.Met {
		return signin.Directory{}, password.refusal()
	}

	return directory, nil
}

// refusal is the Conflict a caller who does not meet the condition is refused with: its
// sentence, then what the caller does.
func (c TenantSignInCondition) refusal() error {
	return httpio.NewConflictMessage(c.Sentence + " " + c.ToDo)
}

// throughDirectory is the first thing turning a tenant directory only takes of the
// caller, said for them: that they are signed in through directory, the tenant's own.
// The session proves the directory works, and the person who closes the tenant to
// every other sign-in is not shut out by it. A caller who is not is told how they did
// sign in, and to sign in through the directory.
func (r *TenantDirectoryRules) throughDirectory(ctx context.Context, directory signin.Directory) TenantSignInCondition {
	name := r.directoryName(directory)
	condition := TenantSignInCondition{
		Sentence: fmt.Sprintf("Directory only is turned on by a person who is signed in through this %s's directory, %s: that shows the directory works, and that you will not shut yourself out.", r.word, name),
	}
	session := sessioninfo.DataFromCtx(ctx)
	established, ok := session.EstablishedBy()
	if ok && (signin.Directory{Method: established.Method, Connection: established.Connection}) == directory {
		condition.Met = true

		return condition
	}
	condition.ToDo = fmt.Sprintf("%s Sign in through %s and try again.", establishedSentence(session, r.namers), name)

	return condition
}

// hasPassword is the second thing turning a tenant directory only takes of the caller,
// said for them: that their own account has a password. applies is false where no auth
// says the question is theirs to answer (PasswordsBy): the caller's auth has a password
// method, and the caller is its person. Where none applies the rule stands down, and
// nothing is said of it.
func (r *TenantDirectoryRules) hasPassword(ctx context.Context, txn ReadWriteTransaction) (condition TenantSignInCondition, applies bool, err error) {
	condition = TenantSignInCondition{
		Sentence: fmt.Sprintf("Directory only is turned on by a person who has a password: an administrator of this %s enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem.", r.word),
		Met:      true,
	}
	for _, read := range r.passwords {
		if read == nil {
			continue
		}
		has, asked, err := read(ctx, txn)
		if err != nil {
			return TenantSignInCondition{}, false, errors.Wrap(err, "resource.CallerPassword()")
		}
		if !asked {
			continue
		}
		applies = true
		if !has {
			condition.Met = false
			condition.ToDo = "Choose a password on Your account first, then try again."
		}
	}

	return condition, applies, nil
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

// SignInSettings answers tenant's sign-in settings as they stand, in the caller's
// transaction, with what a page of them says of how the tenant stands: its directory as
// people know it, the role a person gets the first time they sign in through it, in
// words, whether the caller's auth has passwords, how the caller signed in, whether the
// tenant is directory only, and how many people the tenant seats and how many of them
// the switch keeps out (CountedBy), counted as the warning counts them, with each auth's
// share of both (DirectoryOnlyCount.Shares). While the switch is off it also says what
// turning it on takes of the caller (TenantSignInSettings.Conditions): each condition
// the call would hold them to, whether they meet it, and what they do where they do
// not, in the words the call refuses in. Where the directories record carries the
// second-factor column it says the second factor's part (DirectorySecondFactor). A
// tenant with no directory is refused before anything is asked of the person, so no
// condition is said for it, nobody is counted, and no part of the second factor is
// said. For a tenant record that declares no switch, no flag is read: the tenant is not
// directory only, no condition of the switch is said, and nobody is counted. A tenant
// the record does not hold is NotFound where the record declares the switch; without
// one it reads as a tenant with no directory, and the generated router's guard of the
// tenant's route has answered it NotFound before.
func (r *TenantDirectoryRules) SignInSettings(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantSignInSettings, error) {
	if !r.columns.declared() {
		return nil, errors.New("resource: a tenant's directory rules need the directories record's columns: build them with the generated New<Record>DirectoryRules")
	}
	switched := r.columns.DirectoryOnly != ""
	on := false
	if switched {
		var found bool
		var err error
		if on, found, err = r.switchState(ctx, txn, tenant); err != nil {
			return nil, err
		}
		if !found {
			return nil, httpio.NewNotFoundMessagef("This %s does not exist.", r.word)
		}
	}
	session := sessioninfo.DataFromCtx(ctx)
	settings := &TenantSignInSettings{
		DirectoryOnly:    on,
		SignedIn:         establishedSentence(session, r.namers),
		SessionDirectory: r.sessionDirectoryName(session),
	}
	password, applies, err := r.hasPassword(ctx, txn)
	if err != nil {
		return nil, err
	}
	settings.Passwords = applies
	directory, has, err := r.directoryOf(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if !has {
		return settings, nil
	}
	settings.Directory = r.directoryName(directory)
	if settings.DefaultRole, err = r.defaultRoleInWords(ctx, txn, tenant); err != nil {
		return nil, err
	}
	if settings.SecondFactor, err = r.secondFactorPart(ctx, txn, tenant); err != nil {
		return nil, err
	}
	if !switched {
		return settings, nil
	}
	if settings.People, err = r.count(ctx, txn, tenant); err != nil {
		return nil, err
	}
	if on {
		return settings, nil
	}
	settings.Conditions = append(settings.Conditions, r.throughDirectory(ctx, directory))
	if applies {
		settings.Conditions = append(settings.Conditions, password)
	}

	return settings, nil
}

// sessionDirectoryName says the organization's directory session was signed in through,
// as people know it, where establishedSentence says "You signed in through" it: empty
// for a session signed in with a password or a personal account, or through no
// directory.
func (r *TenantDirectoryRules) sessionDirectoryName(session *sessioninfo.SessionData) string {
	established, ok := session.EstablishedBy()
	directory := signin.Directory{Method: established.Method, Connection: established.Connection}
	// An organization's directory is signed in through neither a password nor a
	// personal account, so this is where the sentence names it.
	if !ok || !directory.Organization() {
		return ""
	}

	return r.directoryName(directory)
}

// defaultRoleInWords says, in words, the role a person gets the first time they sign in
// through tenant's directory, which the tenant has: empty where it admits the
// directory's people by invitation only, or where the directories record declares no
// default seat, which leaves the read with no role to say and no reason to fail.
func (r *TenantDirectoryRules) defaultRoleInWords(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (string, error) {
	if r.columns.DirectoriesKeyColumn == "" || r.columns.DefaultSeatColumn == "" {
		return "", nil
	}
	directory, err := r.Directory(ctx, txn, tenant)
	if err != nil {
		return "", err
	}
	if directory.DefaultRole == "" {
		return "", nil
	}

	return words.RoleInWords(string(directory.DefaultRole)), nil
}

// count sums, over the rules' people, how many people tenant seats and how many of them
// its switch keeps out, and says each auth's share where its entry names its list of
// people (DirectoryOnlyShare), in the rules' order; nil for rules that count nobody.
func (r *TenantDirectoryRules) count(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*DirectoryOnlyCount, error) {
	if len(r.people) == 0 {
		return nil, nil
	}
	count := &DirectoryOnlyCount{}
	for i := range r.people {
		auth := &r.people[i]
		share := DirectoryOnlyShare{Label: auth.Label, People: auth.List}
		if auth.Seated != nil {
			seated, err := auth.Seated(ctx, txn, tenant)
			if err != nil {
				return nil, errors.Wrap(err, "resource.SeatedCount()")
			}
			share.Seated = seated
		}
		if auth.Outsiders != nil {
			outsiders, err := auth.Outsiders(ctx, txn, tenant)
			if err != nil {
				return nil, errors.Wrap(err, "resource.DirectoryOutsiders()")
			}
			share.Outside = len(outsiders)
		}
		count.Seated += share.Seated
		count.Outside += share.Outside
		if auth.List != "" {
			count.Shares = append(count.Shares, share)
		}
	}

	return count, nil
}

// TenantSignInSettingsHandler serves a tenant's sign-in settings as they stand
// (TenantSignInSettings, TenantSignInSettingsRoute): what a tenant's Sign-in settings
// page shows a person who may change any of them, how the tenant stands and how many
// people the switch keeps out, with what each change takes of them (SignInSettings).
// The read is the tenant's own: it is served under the tenant's route and asked under
// the grants of the tenant's own sign-in methods, Execute in the tenant's scope on one
// of methods (the ones that turn directory only, set the default role, or say the
// directory's second factor: the generated handler names every one the application
// serves, on whichever outlet), where a caller with none is Forbidden and learns
// nothing of the tenant. A grant that holds on a condition is
// refused the same way: a read has no request for the condition to judge. It writes
// nothing. client runs it in one transaction, rules are the tenant record's, and
// tenantParam is the route parameter that names the tenant. rules are the ones the
// tenant's own methods are given, each auth's answer about the caller's password with
// them (PasswordsBy), each auth's answer about its second factor (SecondFactorsBy) and
// each auth's people of the tenant (CountedBy), and the read stamps the caller it
// checked before it asks them (WithCaller): rules that ask no auth, or a read that named
// no caller, would leave the password out of what the read says while the method still
// refused for it. The generated handler of a tenant record that declares a directories
// record builds it.
func TenantSignInSettingsHandler(
	client Client, rules *TenantDirectoryRules, userPermissions func(r *http.Request) UserPermissions,
	tenantParam httpio.ParamType, methods ...accesstypes.Resource,
) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		tenant := httpio.Param[accesstypes.Domain](r, tenantParam)
		caller := &Caller{Permissions: userPermissions(r), Scope: accesstypes.DomainScope(tenant), Env: RequestEnvironment()}
		if err := holdsOneOf(ctx, caller, methods); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		// The rules ask each auth whether the caller has a password, and an auth answers
		// for the caller the request stamped: the read stamps the one it checked, as the
		// frame of each of the tenant's own methods does.
		ctx = WithCaller(ctx, caller)
		var settings *TenantSignInSettings
		if err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			var err error
			settings, err = rules.SignInSettings(ctx, txn, tenant)

			return err
		}); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(settings)
	})
}

// holdsOneOf holds the caller to the grant a tenant's read is asked under: Execute on
// one of methods, in the tenant's scope, on no condition. An answer that does not hold
// a method that was asked is no grant of it.
func holdsOneOf(ctx context.Context, caller *Caller, methods []accesstypes.Resource) error {
	decisions, err := caller.Permissions.Check(ctx, caller.Env, caller.Scope, accesstypes.Execute, methods...)
	if err != nil {
		return errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	for _, method := range methods {
		if decisions[method].IsGranted() {
			return nil
		}
	}

	return methodRefused(errors.Newf("scope (%s), user (%s) does not have (%s) on %s", caller.Scope, caller.Permissions.User(), accesstypes.Execute, methods))
}

// listsPeople reports whether the caller may list the people on one of tenant's lists
// (an auth's people, or its invitations), as the list itself asks it: List on the list,
// and List on each of fields, the fields of it that name a person, on no condition. A
// field grant that holds on a condition lists some of the people and not others, so it
// names nobody here; and a declaration that names no list, or no field of it, names
// nobody either. An answer that does not hold a resource that was asked is no grant of
// it.
func listsPeople(ctx context.Context, perms UserPermissions, tenant accesstypes.Domain, list accesstypes.Resource, fields []accesstypes.Tag) (bool, error) {
	if list == "" || len(fields) == 0 {
		return false, nil
	}
	scope := accesstypes.DomainScope(tenant)
	listed, err := perms.Check(ctx, RequestEnvironment(), scope, accesstypes.List, list)
	if err != nil {
		return false, errors.Wrap(err, "resource.UserPermissions.Check()")
	}
	if listed[list].IsDenied() {
		return false, nil
	}
	asked := make([]accesstypes.Resource, 0, len(fields))
	for _, field := range fields {
		asked = append(asked, list.ResourceWithTag(field))
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
