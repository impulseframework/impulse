package session

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// SignInMeans is what a user signs in with: whether they hold a password, and the
// directory sign-ins an identity linked to them can sign them in through. It is what a
// page needs to offer the removal of a password only where it can happen (a password is
// held, and something else signs the person in) and to say what the removal leaves. The
// password's hash is no part of it.
type SignInMeans struct {
	// Password reports whether the user holds a password.
	Password bool `json:"password"`
	// Directories are the sign-in methods the login page offers that an identity linked
	// to the user can sign them in through, in the page's order (signsInThrough). It is
	// empty, never nil, when none can: the user holds no identity link, or only links
	// that sign nobody in any more, and then the password is their only means of
	// sign-in and its removal is refused.
	Directories []LinkedDirectory `json:"directories"`
	// DirectoryAddress reports whether the user signs in through an organization's
	// directory, so their address is the directory's and is not changed here: the
	// judgment a change of the address is refused by (directoryPerson).
	DirectoryAddress bool `json:"directoryAddress"`
	// PasswordDisabled reports that a password does not sign the user in, whether or not
	// they hold one, because the tenants' sign-in settings refuse it: the Auth's check
	// (SetPasswordDisabledCheck) says the tenant policy would refuse it. A page then
	// draws no Password line, and the first password is refused. It is false on an Auth
	// with no check or no password method.
	PasswordDisabled bool `json:"passwordDisabled"`
}

// LinkedDirectory is a sign-in method of the login page that an identity linked to a
// user can sign them in through: the registration's name and the label its button
// carries, as the methods list gives them (MethodList). Where the page lists two
// registrations of one method (two Entra tenants), a link answers the ones that take
// its connection; a method that cannot tell one connection from another answers every
// registration of it.
type LinkedDirectory struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// signsInThrough names the registrations of the login page that an identity of method
// and connection can sign its person in through, in the page's order: the offered
// registrations of that method whose configuration still takes the connection, or
// cannot tell one connection from another (a nil signin.Method.AdmitsConnection). None
// for an identity that signs nobody in: its method is no registration the page offers
// any more, or no registration of it takes the connection (a tenant taken off an Entra
// registration, a Workspace no longer named).
//
// It is the one judgment of whether a link counts as a means of sign-in: the removal of
// a password is refused by it, on an administrator's route and the person's own, the
// unlink of a link is refused by it where the user holds no password, the read of what
// a person signs in with answers by it, and the pages offer the removal by that read.
// The sign-in policy is not asked: it decides a sign-in from a request no link can
// stand in for, and may write. A link the policy would refuse (a tenant's sign-in
// policy that no longer takes the person's directory) therefore still counts.
func (a *Auth[S, U, Tx]) signsInThrough(method sessioninfo.AuthMethod, connection string) []string {
	m := a.registered()
	var names []string
	for _, name := range m.offeredNames() {
		external, ok := m.external[name]
		if !ok || external.Name != method {
			continue
		}
		if external.AdmitsConnection != nil && !external.AdmitsConnection(connection) {
			continue
		}
		names = append(names, name)
	}

	return names
}

// SignsInThrough names the sign-in registrations of the login page that an identity of
// method and connection (a link's Method and Connection) can sign its person in
// through, and none for an identity that signs nobody in: see signsInThrough. The
// generated user management judges a person's links by it, so its refusal of a
// password's removal is this library's.
func (p *AuthAPI[S, U, Tx]) SignsInThrough(method sessioninfo.AuthMethod, connection string) []string {
	return p.auth.signsInThrough(method, connection)
}

// DirectoryName answers what the Auth's sign-in registrations call the directory an
// identity of method and connection came through, for a sentence that names it to a
// person: name is the name a registration that takes the connection gives it (a listed
// Entra tenant's or Workspace's name), "" where none names it, and label is the label
// of the button of the first registration of the login page that takes the connection,
// "" where the page offers none. An organization's own identity provider (the OpenID
// Connect method) is named by the label of its registration where the page gives it
// one: its connection is the key the application registered it under, which no person
// knows it by (connectionSaid). A sentence says the name where there
// is one and otherwise the directory by the button a person signs in to it with, never
// the connection itself, which for an Entra tenant is an id. It reads nothing: the
// answer is what the registrations fixed in code. It closes registration, as every read
// of the Auth's methods does.
func (p *AuthAPI[S, U, Tx]) DirectoryName(method sessioninfo.AuthMethod, connection string) (name, label string) {
	return p.auth.directoryName(method, connection)
}

// TenantName answers what people call a tenant, by its key, from the names the
// application set (SetTenantNames): "" where it set none, for a tenant they do not know
// and for no key. A sentence that names a tenant to a person says it by this and never by
// the key, which is an id. It reads nothing.
func (p *AuthAPI[S, U, Tx]) TenantName(tenant accesstypes.Domain) string {
	return p.auth.tenantName(string(tenant))
}

// SessionTimeout answers how long a session of the Auth may sit idle before it times out:
// what the application set (WithSessionTimeout), and ten minutes where it set none. A list
// that says which sessions are live reads it, so the list and a request are judged by one
// length. It reads nothing.
func (p *AuthAPI[S, U, Tx]) SessionTimeout() time.Duration {
	return p.auth.baseSession.SessionTimeout
}

// directoryName is DirectoryName: the registrations of the login page first, in its
// order, then every other by name, as SignInMethods lists them.
func (a *Auth[S, U, Tx]) directoryName(method sessioninfo.AuthMethod, connection string) (name, label string) {
	m := a.registered()
	names := slices.Clone(m.offeredNames())
	for _, registered := range slices.Sorted(mapKeys(m.registered)) {
		if !slices.Contains(names, registered) {
			names = append(names, registered)
		}
	}
	for _, registered := range names {
		external, ok := m.external[registered]
		if !ok || external.Name != method {
			continue
		}
		if external.AdmitsConnection != nil && !external.AdmitsConnection(connection) {
			continue
		}
		if name == "" {
			name = m.connectionSaid(registered, connection)
		}
		if label == "" {
			label = m.labels[registered]
		}
	}

	return name, label
}

// connectionSaid is the name the external registration under registered gives the
// organization behind connection, as people read it: the registration's label where its
// method leaves the naming to it (labelName), and otherwise the method's own name for
// the connection, "" where it has none.
func (m *signInMethods[Tx]) connectionSaid(registered, connection string) string {
	if said := m.labelName(registered); said != "" {
		return said
	}
	external := m.external[registered]
	if external.ConnectionName == nil {
		return ""
	}

	return external.ConnectionName(connection)
}

// signsIn reports whether an identity of method and connection counts as a means of
// sign-in (signsInThrough): what an unlink requires of another of the user's links, in
// the transaction that unlinks, where the user holds no password.
func (a *Auth[S, U, Tx]) signsIn(method sessioninfo.AuthMethod, connection string) bool {
	return len(a.signsInThrough(method, connection)) > 0
}

// linksThatSignIn are the ids of the links that count as a means of sign-in (signsIn):
// what a removal of the user's password requires one of to still stand when it writes.
func (a *Auth[S, U, Tx]) linksThatSignIn(links []*sessionstorage.SessionIdentity) []impulse.UUID {
	var standing []impulse.UUID
	for _, link := range links {
		if a.signsIn(link.Method, link.Connection) {
			standing = append(standing, link.ID)
		}
	}

	return standing
}

// SignInMeans answers what a user signs in with (see SignInMeans the type): for an
// administrator's page, or a flow of the application's own. The caller guards who may
// ask about whom. A user that does not exist is a NotFound. Each method carries the
// label it was first listed under.
func (p *AuthAPI[S, U, Tx]) SignInMeans(ctx context.Context, userID impulse.UUID) (*SignInMeans, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return p.auth.signInMeans(ctx, userID, p.auth.registered().labels)
}

// signInMeans reads what userID signs in with, each method under its label in labels.
func (a *Auth[S, U, Tx]) signInMeans(ctx context.Context, userID impulse.UUID, labels map[string]string) (*SignInMeans, error) {
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	links, err := a.storage.IdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}
	means := a.signInMeansOf(user, links, labels)
	if means.PasswordDisabled, err = a.passwordDisabled(ctx, userID); err != nil {
		return nil, err
	}

	return means, nil
}

// passwordDisabled answers whether the tenants' sign-in settings disable userID's
// password: the Auth's check (SetPasswordDisabledCheck), and false, with nothing asked,
// on an Auth that holds no check or registers no password method. The check is handed
// the directories the account signs in through, read with the Auth's directory reader
// (SetDirectoryAdmission), and none, with the store not asked, on an Auth that holds
// no reader. The read of what a person signs in with and the first password ask it.
func (a *Auth[S, U, Tx]) passwordDisabled(ctx context.Context, userID impulse.UUID) (bool, error) {
	if a.passwordDisabledCheck == nil || !a.registered().passwordRegistered() {
		return false, nil
	}
	var directories []sessioninfo.UserDirectory
	if a.directories != nil {
		read, err := a.storage.UserDirectories(ctx, userID, a.directories)
		if err != nil {
			return false, errors.Wrap(err, "sessionstorage.AuthStore.UserDirectories()")
		}
		directories = read
	}
	disabled, err := a.passwordDisabledCheck(ctx, userID, directories)
	if err != nil {
		return false, errors.Wrap(err, "signin.PasswordDisabledCheck()")
	}

	return disabled, nil
}

// signInMeansOf is what user signs in with, given the identities linked to them: the
// registrations a link can sign them in through (signsInThrough), each once, in the
// page's order and under its label in labels, and whether a link came through an
// organization's directory (organizationLinked).
func (a *Auth[S, U, Tx]) signInMeansOf(user *sessionstorage.SessionUser, links []*sessionstorage.SessionIdentity, labels map[string]string) *SignInMeans {
	reached := map[string]bool{}
	for _, link := range links {
		for _, name := range a.signsInThrough(link.Method, link.Connection) {
			reached[name] = true
		}
	}
	means := &SignInMeans{Password: user.PasswordHash != nil, Directories: []LinkedDirectory{}, DirectoryAddress: organizationLinked(links)}
	for _, name := range a.registered().offeredNames() {
		if reached[name] {
			means.Directories = append(means.Directories, LinkedDirectory{Name: name, Label: labels[name]})
		}
	}

	return means
}

// SignInMeans is the handler a signed-in person's account page reads what they sign in
// with from: {"password", "directories": [{"name", "label"}], "directoryAddress",
// "passwordDisabled"} (see SignInMeans the type), for the session's user and nobody
// else. The page hides the change and the removal of a password from a person who holds
// none, offers the removal only where a directory sign-in can sign the person in, names
// what the removal leaves by its label, and offers no change of an address that is a
// directory's. The server's own refusals stand whatever the page shows.
//
// The handler validates the session itself, as the name change does, so it is routed
// beside it, outside ValidateSession, and its path is named to the page as the methods
// list's SignInMeans. A session that acts for someone else is answered for the person
// it acts as, as the session read is. The labels are those of the methods list that
// names the path the read is served at, so an Auth served on two outlets, each with a
// list of its own, answers each outlet's page in that outlet's words; a read served at
// a path no list names, or one that two lists name, answers each method under the
// label it was first listed under.
func (a *Auth[S, U, Tx]) SignInMeans() http.HandlerFunc {
	return a.serving(a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		if _, validated := ctx.Value(sessioninfo.CtxUserInfo).(*sessioninfo.UserInfo); !validated {
			var err error
			if ctx, err = a.validateRequest(ctx); err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
		}
		means, err := a.signInMeans(ctx, sessioninfo.UserFromCtx(ctx).ID, a.registered().labelsServedAt(r.URL.Path))
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(means)
	}))
}
