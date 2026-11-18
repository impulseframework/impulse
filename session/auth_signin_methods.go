package session

import (
	"slices"

	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// RegisteredMethod is one sign-in method registered on an Auth, as the code that
// registered it fixed it: what the page where an administrator opens a method to
// sign-up, or makes it invite only, lists beside the administrator's choice. The choice
// itself is no part of it: it is a row of the Auth's sign-in methods table
// (Tables.SignInMethods), keyed by Name.
type RegisteredMethod struct {
	// Name is the name the method is registered under on the Auth: "password", or the
	// application's name for an external registration ("google", "lakeside-entra"). It
	// is the key of the method's row of the sign-in methods table.
	Name string
	// Method is the method behind the registration (password, azure, google, oidc).
	Method sessioninfo.AuthMethod
	// Label is what the login page shows for the method, "" for a method the login
	// page does not offer.
	Label string
	// Audience is how wide the method is: the organizations it is limited to, or the
	// kind of account it admits. It is whom opening the method to sign-up admits.
	Audience signin.Audience
	// SyncsRoles reports a method whose directory writes the roles of the people it
	// signs in. Such a method has no choice to make: the directory says who holds a
	// role, a person it gives one is admitted, and one it gives none is refused.
	SyncsRoles bool
	// OwnRule reports a registration that carries an account rule of its own
	// (signin.AccountRule): the application's code decides who may sign in through it,
	// so an administrator neither opens it to sign-up nor makes it invite only.
	OwnRule bool
}

// SignInMethods lists the sign-in methods registered on the Auth, in the login page's
// order, then every other registration by name. It reads nothing: the answer is what
// the registrations fixed in code. It closes registration, as every read of the Auth's
// methods does.
func (p *AuthAPI[S, U, Tx]) SignInMethods() []RegisteredMethod {
	m := p.auth.registered()
	names := slices.Clone(m.offeredNames())
	for _, name := range slices.Sorted(mapKeys(m.registered)) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	methods := make([]RegisteredMethod, 0, len(names))
	for _, name := range names {
		method := RegisteredMethod{Name: name, Method: sessioninfo.MethodPassword, Label: m.labels[name], Audience: passwordAudience()}
		if external, ok := m.external[name]; ok {
			method.Method, method.Audience, method.SyncsRoles, method.OwnRule = external.Name, external.Audience, external.SyncRoles != nil, external.rule != nil
		}
		methods = append(methods, method)
	}

	return methods
}

// identityEmailAddress is what identifies whom the password method admits, as an
// audience's key spells it: anyone who proves an email address.
const identityEmailAddress = "password:any-email-address"

// passwordAudience is how wide the password method is: a person signs up with a
// password by proving an email address, so it admits anyone who has one.
func passwordAudience() signin.Audience {
	return signin.Audience{Organizations: nil, Accounts: "an email address", Identities: []string{identityEmailAddress}}
}

// mapKeys yields the keys of a set of names.
func mapKeys(set map[string]bool) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for name := range set {
			if !yield(name) {
				return
			}
		}
	}
}
