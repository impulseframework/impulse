// Package signin is the contract between the Auth session and a sign-in method.
//
// The Auth session (the core) owns sessions, users, identities, the pending flow and
// the middleware. A sign-in method is a package that knows one way to establish who the
// person is: a password form in the core itself, or a redirect to an identity provider
// such as Microsoft Entra ID, Google Workspace or WorkOS. An external method registers
// on the core with Register and hands it a Method: how to begin the round trip to the
// provider, how to complete it into a verified identity, and how to synchronize the
// user's roles when the provider asserts them. The core builds the HTTP handlers, runs
// resolution, the sign-in policy and the session the same way for every method, and
// returns the handlers to the method.
//
// The package also holds the vocabulary of a sign-in's outcomes, which the application's
// hooks speak: a custom AccountRule's Resolution, the sign-in policy's Decision and the
// causes of a refusal.
//
// A method package imports this package and never the core.
package signin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/cookie"
	"github.com/impulseframework/impulse/session/internal/vouch"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// Core is what a method registers on: the Auth that session.NewAuth returned.
// *session.Auth implements it. Tx is the Auth's storage transaction type, which the
// method's account rule receives.
type Core[Tx any] interface {
	// RegisterExternal registers a redirect-based method and returns its handlers. It
	// panics on a registration rule, because that is a wiring mistake found at startup:
	// the registration has no name or its name is already used, the core has served a
	// request, or reg.Err carries a misuse the registration function found in its
	// arguments.
	RegisterExternal(reg *Registration[Tx]) Handlers
}

// Registration is what a method asks the core to register.
type Registration[Tx any] struct {
	// Name identifies the registration on the core, where it must be unique. One
	// method may be registered several times under different names (two Entra
	// tenants); the name is the application's, such as "azure" or "lakeside-entra".
	Name string
	// Method names the method; it is the identity's method and the session's auth
	// event's.
	Method sessioninfo.AuthMethod
	// AccountRule is the application's own rule for an identity of the method that is
	// not linked to any user yet, nil for none, which leaves every such identity to the
	// library's standard rule:
	// the method builds it over the rule the application gave its registration, typed
	// to the method's identity.
	AccountRule AccountRule[Tx]
	// SeededUsers declares that the provider's names are the application's own: an
	// identity of this registration under the name of a user created ahead is that
	// user, so the standard rule links the user on the name alone, whatever tenant has
	// the provider as its directory and whatever the user holds. It is for the provider
	// a deployment runs for the accounts it seeds, as the persona provider of the test
	// environments is for the cast, and never for an organization's directory, whose
	// names are the organization's to assert.
	SeededUsers bool
	// Build builds the method over the registration's state cookie, which carries the
	// browser's half of a round trip to the provider; the core names it for the
	// registration.
	Build func(cookies StateCookies) (*Method, error)
	// FrontChannelLogout asks for the provider-initiated logout handler.
	FrontChannelLogout bool
	// Issuers is the set of issuers the registration's tokens may come from, nil for a
	// method that has none. The core refuses a round trip whose Verified.Issuer is not
	// in the set, records a sign-in's provider session id under that issuer (a
	// provider's session id is unique only within its issuer), and scopes the
	// front-channel logout handler by one of the set: the issuer the request names
	// (iss), or the set's only issuer when the request names none. A registration that
	// asks for the handler must declare its issuers; one that declares none records no
	// provider session id.
	Issuers Issuers
	// Err is a misuse the registration function found in its own arguments; the core
	// panics with it.
	Err error
}

// StateCookies writes and reads a registration's state cookie: the state and PKCE
// verifier of a round trip to the provider, and the return URL the browser goes to
// afterwards. The core names the cookie for the registration (OIDC-<name>) and binds it
// to the registration, so two registrations of one method, and logins with different
// providers in one browser, never read each other's state. ReadStateCookie's readName
// is the cookie's name, or "" when the browser carries none for this registration;
// a state cookie is single-use, so the method passes readName to DeleteStateCookie.
type StateCookies interface {
	WriteStateCookie(w http.ResponseWriter, values *cookie.Values)
	ReadStateCookie(r *http.Request) (values *cookie.Values, readName string, err error)
	DeleteStateCookie(w http.ResponseWriter, name string)
}

// Method is a registered external method as the core drives it.
type Method struct {
	// Name is the method's name.
	Name sessioninfo.AuthMethod
	// LoginURL is where refused and pending sign-ins are sent.
	LoginURL func() string
	// Begin writes the method's state cookie and returns the provider URL the browser
	// goes to. returnURL is the sanitized path in the application the browser returns
	// to afterwards.
	Begin func(ctx context.Context, w http.ResponseWriter, r *http.Request, returnURL string) (string, error)
	// Complete verifies the provider's answer into a verified sign-in. Every refusal is a
	// sessioninfo.LoginRefusal.
	Complete func(ctx context.Context, w http.ResponseWriter, r *http.Request) (*Verified, error)
	// SyncRoles reconciles the user's roles to the role names the provider asserted,
	// and refuses the sign-in (no_roles) when no recognized role results. The user is
	// named as the permission store names them, by the id of their account. It is nil
	// when the method synchronizes no roles.
	SyncRoles func(ctx context.Context, user accesstypes.User, roleNames []string) error
	// RecognizesRoles reports whether SyncRoles would leave the user holding a role:
	// at least one of roleNames is a role the application knows. The core asks it
	// before the sign-in is resolved, so a person whose roles admit nothing is refused
	// (no_roles) before anything is written or an invitation they carry is consumed.
	// It is nil when the method synchronizes no roles or cannot tell beforehand;
	// SyncRoles then refuses once the session stands.
	RecognizesRoles func(ctx context.Context, roleNames []string) (bool, error)
	// AdmitsAddress reports whether an identity with this email address could be
	// admitted by the method, which the core asks before it sends an invitation to the
	// address. It is nil when the method cannot tell, which admits.
	AdmitsAddress func(email string) bool
	// Audience is how wide the method is, which its registration fixes: the
	// organizations it is limited to, or the kind of account it admits. It is what
	// opening the method to sign-up admits.
	Audience Audience
	// AdmitsConnection reports whether an identity that came through connection (the
	// Connection its link carries) could sign in through this registration as it is
	// configured now: a tenant, a Workspace or a provider the registration still takes.
	// The core asks it to judge whether a link is still a means of sign-in, before a
	// person's password is removed on the strength of it. It is nil when the method
	// cannot tell one connection from another, which admits.
	AdmitsConnection func(connection string) bool
	// ConnectionName answers the name the registration gives the organization behind a
	// connection, in words a person knows: the application's name for a listed
	// Microsoft Entra tenant, a Google Workspace's name or the domain of one admitted
	// as any Workspace, the application's name for an OpenID Connect provider or the
	// host of its issuer. It answers "" for a connection the registration has no name
	// for, a tenant listed by its id alone or admitted as any tenant: a sentence then
	// says the directory by the label of the registration's button, never by the id.
	// It is nil when the method names no connection. The Auth answers a directory's
	// name from it (session.AuthAPI.DirectoryName).
	ConnectionName func(connection string) string
	// AddressAuthority is the library's mark on the one method whose word that an
	// address is verified the core keeps (sessioninfo.Identity.EmailVerified): Google's,
	// which session/google builds. The core knows Google's method by this mark and never
	// by its name. Only the session library can make a mark, so a method an
	// application builds leaves the field as it is, the zero value, which is no mark:
	// the core then takes the flag off every identity the method verifies, whatever the
	// method is named.
	AddressAuthority vouch.Mark
}

// Verified is the result of a method's completed round trip.
type Verified struct {
	// Identity is who the provider says signed in, keyed by the method, its connection
	// and the provider's immutable subject, with the sign-in name the standard account
	// rules use and whether the account is a guest of its directory.
	Identity *sessioninfo.Identity
	// ReturnURL is the sanitized path in the application the browser goes to afterwards.
	ReturnURL string
	// RoleNames are the role names the provider asserted. They are nil when the method
	// synchronizes no roles, and never nil when it does: "no roles asserted" is an empty
	// list.
	RoleNames []string
	// ProviderSessionID is the provider's own session id for this sign-in (an OpenID
	// Connect sid claim), which a provider-initiated logout names; "" when the method
	// has none.
	ProviderSessionID string
	// Issuer is the issuer of the token the round trip verified, "" when the method
	// has none. It must be one of the registration's Issuers when those are declared;
	// the core records ProviderSessionID under it.
	Issuer string
}

// Handlers are a registered method's HTTP handlers, built by the core.
type Handlers struct {
	// Login begins the round trip: it redirects the browser to the provider.
	Login http.HandlerFunc
	// Callback completes it: it resolves the identity, applies the sign-in policy and
	// establishes the session, holds it pending, or refuses it.
	Callback http.HandlerFunc
	// FrontChannelLogout handles a provider-initiated logout; nil unless the
	// registration asked for it.
	FrontChannelLogout http.HandlerFunc
}

// Register registers reg on core and returns its handlers. The registration functions
// of every method go through it: it refuses a nil core before the core can.
func Register[Tx any](core Core[Tx], reg *Registration[Tx]) Handlers {
	if core == nil {
		panic(fmt.Sprintf("session: the %s sign-in method is registered on a nil Auth: pass the Auth NewAuth returned", reg.Method))
	}

	return core.RegisterExternal(reg)
}

// AddressDomain is the domain of an email address with its ASCII letters lowercased:
// what follows its last @, or "" when it has none. A method's AdmitsAddress judges an
// address by it, and Google's method tells an address of Google's own mail by it. Only
// ASCII letters are lowercased: Unicode lowercases some letters from outside ASCII to
// ASCII ones (the dotted capital I, U+0130, to i; the Kelvin sign, U+212A, to k), and a
// domain written with one is another domain than the ASCII one it would then read as,
// so it is left as it was written and equals no ASCII domain.
func AddressDomain(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return ""
	}

	return strings.Map(lowerASCII, email[i+1:])
}

// lowerASCII is r lowercased when it is an ASCII capital letter, and r otherwise.
func lowerASCII(r rune) rune {
	if 'A' <= r && r <= 'Z' {
		return r + ('a' - 'A')
	}

	return r
}

// RoleNames returns names, or an empty list for nil. A role-synchronizing method always
// carries a list, so "no roles asserted" stays distinct from "no role sync".
func RoleNames(names []string) []string {
	if names == nil {
		return []string{}
	}

	return names
}
