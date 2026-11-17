package identity

import (
	"context"
	"slices"
	"strings"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// Who may sign in without an invitation.
//
// Whether a person with no account and no invitation may sign in is one choice an
// administrator makes in the running application, for each sign-in method of the auth:
// invite only, or open to sign-up. The choice is the method's row of the auth's sign-in
// methods table, which the session library reads in the sign-in's own transaction; a
// method with no row is invite only. The rules here are the ones the choice is made
// under: the method is one the auth registers, a method whose directory gives the
// roles has no choice to make, and an application without tenants cannot open a method
// without the role each new person is given. The methods the generator writes over
// them carry the grant that says who may make the choice, and the generated patch keeps
// who made it and when.
//
// How wide a method is, which is whom opening it admits, is fixed in code where the
// method is registered and is read here from the session library (SessionAPI.SignInMethods)
// to say it in words: on the page that lists the methods, and in the warning an
// administrator reads before a method is opened.

// SignInMethodRow is a sign-in method's row of the sign-in methods table, as the rules
// read and write it.
type SignInMethodRow struct {
	// Name is the name the method is registered under.
	Name string
	// OpenSignUp says the method admits a person who holds no account and no invitation.
	OpenSignUp bool
	// DefaultRole is the role such a person is given with their account, nil for none.
	DefaultRole *string
}

// SignInMethod is one sign-in method the auth registers, with what an administrator
// chose for it, as the Sign-in page lists it.
type SignInMethod struct {
	// Name is the name the method is registered under, and Label what the login page
	// shows for it.
	Name  string
	Label string
	// Admits is how wide the method is, as a sentence starts: "Anyone with an account
	// at lakeside.edu", "Anyone with a Google account". It is whom opening the method
	// to sign-up admits.
	Admits string
	// OpenSignUp says the method is open to sign-up; a method that is not is invite
	// only. DefaultRole is the role a new person is given through an open method, ""
	// for none.
	OpenSignUp  bool
	DefaultRole string
	// DirectoryGivesRoles marks a method whose directory gives the roles: the directory
	// decides who may sign in, and the method is neither opened nor made invite only.
	DirectoryGivesRoles bool
	// Says is the row's state as a sentence a person reads: what a person with no
	// invitation meets at this method.
	Says string
}

// SignUp is what opening a sign-in method to sign-up asks for.
type SignUp struct {
	// Method is the name the method is registered under.
	Method string
	// DefaultRole is the role each person who signs up is given, a role held for the
	// whole application. An application without tenants names one; an application with
	// tenants names none.
	DefaultRole accesstypes.Role
	// CreatesTenant marks an application with tenants: a person who signs up holds no
	// role, and creates a tenant of their own, which they administer.
	CreatesTenant bool
}

// SignInMethods lists the sign-in methods the auth registers, in the login page's
// order, each with what an administrator chose for it. reader is the caller's
// transaction or the application's client: the rows are read as committed.
func (m *Manager) SignInMethods(ctx context.Context, reader resource.ReadOnlyTransaction) ([]SignInMethod, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	rows, err := m.store.SignInMethodRows(ctx, reader)
	if err != nil {
		return nil, errors.Wrap(err, "identity.Store.SignInMethodRows()")
	}
	registered := m.sessions.SignInMethods()
	methods := make([]SignInMethod, 0, len(registered))
	for i := range registered {
		methods = append(methods, m.signInMethod(&registered[i], rowNamed(rows, registered[i].Name)))
	}

	return methods, nil
}

// AnyMethodOpen reports whether an administrator has some sign-in method of the auth
// open to sign-up now. In an application with tenants it is what lets a person who
// holds no tenant create one: a person who signed up through an open method, and nobody
// while every method is invite only.
func (m *Manager) AnyMethodOpen(ctx context.Context, reader resource.ReadOnlyTransaction) (bool, error) {
	methods, err := m.SignInMethods(ctx, reader)
	if err != nil {
		return false, err
	}

	return slices.ContainsFunc(methods, func(method SignInMethod) bool { return method.OpenSignUp }), nil
}

// signInMethod is one registered method with its row, nil for a method that has none.
func (m *Manager) signInMethod(registered *session.RegisteredMethod, row *SignInMethodRow) SignInMethod {
	method := SignInMethod{
		Name: registered.Name, Label: registered.Label, Admits: sentenceStart(registered.Audience.Words()),
		DirectoryGivesRoles: registered.SyncsRoles,
	}
	switch {
	case registered.SyncsRoles:
		method.Says = "The directory gives the roles, so it decides who may sign in: a person it gives a role signs in, and nobody else."
	case row != nil && row.OpenSignUp:
		method.OpenSignUp = true
		if row.DefaultRole != nil {
			method.DefaultRole = *row.DefaultRole
		}
		method.Says = "Open: " + m.consequence(registered, method.DefaultRole, method.DefaultRole == "")
	default:
		method.Says = "Invite only: a person signs in once they are invited."
	}

	return method
}

// SignUpRoles lists the roles a person who signs up may be given: the roles held for the
// whole application, by name, the role file's and the custom ones. The Super
// Administrator role is not among them: it is given by a super administrator, to a
// person they name.
func (m *Manager) SignUpRoles(ctx context.Context) ([]access.RoleInfo, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	roles, err := m.access.UserManager().Roles(ctx, accesstypes.GlobalPolicyScope())
	if err != nil {
		return nil, errors.Wrap(err, "access.UserManager.Roles()")
	}

	return slices.DeleteFunc(roles, func(role access.RoleInfo) bool { return role.Name == accesstypes.SuperAdministrator }), nil
}

// OpenSignUp buffers the choice that opens a sign-in method to sign-up, into the
// caller's transaction: from the commit on, a person with no account and no invitation
// who signs in through the method gets an account. It refuses a method the auth does
// not register and a method whose directory gives the roles. In an application without
// tenants it refuses a request that names no default role, and one whose role is not
// held for the whole application or is the Super Administrator role: nobody signs up
// holding nothing, and nobody signs up administering the application. In an
// application with tenants (CreatesTenant) it refuses a request that names a role,
// since a person who signs up there creates a tenant of their own.
func (m *Manager) OpenSignUp(ctx context.Context, txn resource.ReadWriteTransaction, open *SignUp) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if _, err := m.choosable(open.Method); err != nil {
		return err
	}
	row := &SignInMethodRow{Name: open.Method, OpenSignUp: true}
	switch {
	case open.CreatesTenant && open.DefaultRole != "":
		return httpio.NewBadRequestMessagef("no default role is chosen here: a person who signs up creates %s of their own and administers it, so send the request without a role", withArticle(m.tenantWord))
	case open.CreatesTenant:
	case open.DefaultRole == "":
		return httpio.NewBadRequestMessage("choose the default role: each person who signs up is given one, so a sign-in method cannot be opened without it")
	default:
		if err := m.requireSignUpRole(ctx, txn, open.DefaultRole); err != nil {
			return err
		}
		role := string(open.DefaultRole)
		row.DefaultRole = &role
	}
	if err := m.store.SetSignInMethod(ctx, txn, row); err != nil {
		return errors.Wrap(err, "identity.Store.SetSignInMethod()")
	}

	return nil
}

// OpenSignUpWarning says what opening the method would do, in the words a person reads
// before they confirm it, and the words for the button that does it. It writes
// nothing. It refuses what OpenSignUp refuses of the method, and of a role that is
// named; a request that names no role yet is answered, since the warning is read while
// the role is still being picked, and says the role is to be chosen.
func (m *Manager) OpenSignUpWarning(ctx context.Context, txn resource.ReadWriteTransaction, open *SignUp) (*resource.Warning, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	registered, err := m.choosable(open.Method)
	if err != nil {
		return nil, err
	}
	if !open.CreatesTenant && open.DefaultRole != "" {
		if err := m.requireSignUpRole(ctx, txn, open.DefaultRole); err != nil {
			return nil, err
		}
	}

	return &resource.Warning{
		Consequence: m.consequence(registered, string(open.DefaultRole), open.CreatesTenant),
		Confirm:     "Open to " + registered.Audience.Words(),
	}, nil
}

// MakeInviteOnly buffers the choice that makes a sign-in method invite only again, into
// the caller's transaction: from the commit on, a person with no account needs an
// invitation. The people who have accounts keep them, with what they hold. It refuses a
// method the auth does not register and a method whose directory gives the roles; a
// method that is invite only already is left as it is.
func (m *Manager) MakeInviteOnly(ctx context.Context, txn resource.ReadWriteTransaction, method string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if _, err := m.choosable(method); err != nil {
		return err
	}
	rows, err := m.store.SignInMethodRows(ctx, txn)
	if err != nil {
		return errors.Wrap(err, "identity.Store.SignInMethodRows()")
	}
	if row := rowNamed(rows, method); row == nil || !row.OpenSignUp {
		return nil
	}
	if err := m.store.SetSignInMethod(ctx, txn, &SignInMethodRow{Name: method}); err != nil {
		return errors.Wrap(err, "identity.Store.SetSignInMethod()")
	}

	return nil
}

// choosable answers the registered method of the name, and refuses a name the auth
// registers no method under and a method whose directory gives the roles.
func (m *Manager) choosable(name string) (*session.RegisteredMethod, error) {
	registered := m.sessions.SignInMethods()
	i := slices.IndexFunc(registered, func(method session.RegisteredMethod) bool { return method.Name == name })
	if i < 0 {
		names := make([]string, 0, len(registered))
		for j := range registered {
			names = append(names, registered[j].Name)
		}

		return nil, httpio.NewNotFoundMessagef("the %s auth has no sign-in method named %q: its methods are %s", m.auth, name, strings.Join(names, ", "))
	}
	if registered[i].SyncsRoles {
		return nil, httpio.NewConflictMessagef("the sign-in method %q takes its roles from its directory, which decides who may sign in: it is neither opened to sign-up nor made invite only here", name)
	}

	return &registered[i], nil
}

// requireSignUpRole holds a default role to its rule: a role held for the whole
// application, by the rule a membership's role is held to, and never the Super
// Administrator role.
func (m *Manager) requireSignUpRole(ctx context.Context, txn resource.ReadWriteTransaction, role accesstypes.Role) error {
	if role == accesstypes.SuperAdministrator {
		return httpio.NewBadRequestMessagef("the %s role is never a default role: every person who signed up would administer the application; choose another role", string(role))
	}
	exists, err := m.roleExistsIn(ctx, txn, role, []accesstypes.PolicyScope{accesstypes.GlobalPolicyScope()})
	if err != nil {
		return err
	}
	if !exists {
		return httpio.NewBadRequestMessagef("%q is not a role held for the whole application: choose one of the roles the picker lists", string(role))
	}

	return nil
}

// consequence is what a method open to sign-up means for the people it admits, as the
// warning says it before the method is opened and the page says it while it is open:
// who may sign in, and what each of them gets.
func (m *Manager) consequence(registered *session.RegisteredMethod, role string, createsTenant bool) string {
	who := sentenceStart(registered.Audience.Words())
	act := "sign in"
	if registered.Method == sessioninfo.MethodPassword {
		act = "sign up"
	}
	switch {
	case createsTenant:
		return who + " will be able to sign up and create " + withArticle(m.tenantWord) + " of their own, without an invitation."
	case role == "":
		return who + " will be able to " + act + " without an invitation. Choose the role each of them gets."
	default:
		return who + " will be able to " + act + " without an invitation. Each of them gets the role " + role + "."
	}
}

// SignedUp answers the session library's hook for a sign-up through an open method
// (session.Auth.SetSignUpSeated): the default role the method's row names is buffered
// as a membership of the person for the whole application, in the transaction that
// makes the account. A role that cannot be given (it was deleted since the method was
// opened) fails the transaction, so no account is made holding nothing. The role is
// given by the application itself, outside any request's caller, and is held to the
// rule a role given by hand is, except that it is never the Super Administrator role,
// which the hook refuses whatever the row says.
//
// auth names the Auth in the log line a given role leaves.
func (m *Manager) SignedUp(auth string) session.SignUpSeatedHook[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, tx *spanner.ReadWriteTransaction, userID impulse.UUID, username, method, role string) (func(ctx context.Context), error) {
		if accesstypes.Role(role) == accesstypes.SuperAdministrator {
			return nil, errors.Newf("the sign-in method %q names %s as its default role, which nobody is given by signing up: make the method invite only, then open it with another role", method, role)
		}
		txn := resource.NewSpannerReadWriteTransaction(tx)
		if err := m.AssignRole(ctx, txn, accesstypes.GlobalPolicyScope(), userID, accesstypes.Role(role)); err != nil {
			return nil, errors.Wrapf(err, "the sign-in method %q gives %q its default role %s", method, username, role)
		}

		return func(ctx context.Context) {
			txn.Committed(ctx)
			logger.FromCtx(ctx).Infof("%s: %q signed up through %s and holds %s", auth, username, method, role)
		}, nil
	}
}

// rowNamed is the row of the method among rows, nil when it has none.
func rowNamed(rows []SignInMethodRow, name string) *SignInMethodRow {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}

	return nil
}

// withArticle is the word with its article, as a sentence names one of them: "a
// sector", "an outlet".
func withArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiouAEIOU", rune(word[0])) {
		return "an " + word
	}

	return "a " + word
}

// sentenceStart is words with their first letter in upper case, as a sentence starts.
func sentenceStart(words string) string {
	if words == "" {
		return words
	}

	return strings.ToUpper(words[:1]) + words[1:]
}
