// Package persona is the persona sign-in a host application offers the test environments
// of the applications it knows: an OpenID Connect provider that signs a person in as one
// of the personas an application's deploy registered, after the host has signed the
// person in and said they may enter that application.
//
// The host is the origin of record. It answers two questions through Host: which
// application a client ID belongs to, with the deploy identities allowed to register for
// it, and whether the signed-in person may enter that application. The module owns the
// rest: the registrations the deploys send (redirect addresses and the cast of personas,
// one registration per deployed environment or pull request), the protocol's state
// between requests (authorize requests, codes, access tokens, signing keys), the picker,
// and the log of who signed in as which persona into which client.
//
// An application registers the generic OpenID Connect method of the session library
// (session/oidc) against the host's issuer as a public client, and the browser's round
// trip runs the authorization code flow with PKCE: the application sends the browser to
// the authorize endpoint, the host's session says who the person is, the picker shows the
// cast, and the pick issues the code the application exchanges for an ID token whose
// subject is the persona's login.
package persona

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Host is what the persona sign-in asks of the application it runs in. The host
// implements it over its own records; the module never stores what the host knows.
type Host interface {
	// ApplicationByClient resolves a client ID root, the client ID the host minted when
	// it created the application, to the application: its identity, its tenant, its
	// name, and the deploy identities allowed to register for it. It answers
	// ErrUnknownApplication for a root the host never minted.
	ApplicationByClient(ctx context.Context, clientID string) (*Application, error)
	// MayEnter says whether the signed-in person may sign into the application's test
	// environments. The deployment names the environment or pull request the browser
	// came from, so a host can narrow access by environment later.
	MayEnter(ctx context.Context, actor *Actor, app *Application, deployment *Deployment) (bool, error)
}

// Application is an application as the host knows it.
type Application struct {
	// ID identifies the application in the host's records.
	ID string
	// Tenant is the tenant the application belongs to, as the host names it; "" when the
	// host has no tenants.
	Tenant string
	// Name is what the picker calls the application.
	Name string
	// ClientID is the client ID root the host minted for the application. Every
	// deployment's client ID starts with it (ClientIDFor).
	ClientID string
	// DeployIdentities are the identities allowed to register the application's
	// deployments: the email addresses of the service accounts its deploys run as.
	DeployIdentities []string
}

// Actor is the signed-in person the host's session names: the one who picks a persona.
type Actor struct {
	// ID identifies the person in the host's records.
	ID string
	// Name and Email are what the log records beside the ID.
	Name  string
	Email string
}

// Deployment names one deployed environment or pull request of an application.
type Deployment struct {
	// ClientID is the deployment's own client ID: the application's root with the
	// environment or the pull request appended (ClientIDFor).
	ClientID string `json:"clientId"`
	// Environment is the environment the deployment serves (tst).
	Environment string `json:"environment"`
	// PullRequest is the pull request's number for a pull-request environment, 0 for
	// an environment's own deployment.
	PullRequest int `json:"pullRequest"`
}

// Persona is one member of a cast: a seeded account of the application a person may sign
// in as. The application seeds the account and its roles; the provider asserts the login.
type Persona struct {
	// Login is the persona's username in the application, the subject of the ID token.
	Login string `json:"login"`
	// Name is the persona's display name.
	Name string `json:"name"`
	// Email is the persona's address, as the seed records it.
	Email string `json:"email"`
	// Roles are the roles the seed gives the persona, shown on the picker so the
	// person knows what they are choosing; the provider never asserts them.
	Roles []string `json:"roles"`
}

// Registration is what a deploy registers: the deployment, where the provider may send
// the browser back, and the cast. It is replaced on every deploy of the deployment and
// removed when the deployment is torn down, or by the sweep after its expiry when the
// teardown never ran.
type Registration struct {
	Deployment
	// ApplicationClientID is the application's client ID root the registration was
	// made under.
	ApplicationClientID string `json:"applicationClientId"`
	// RedirectURIs are the callback addresses the provider may send the browser to,
	// matched exactly.
	RedirectURIs []string `json:"redirectUris"`
	// Cast are the personas the deployment seeded.
	Cast []Persona `json:"cast"`
	// RegisteredBy is the deploy identity that made the registration.
	RegisteredBy string `json:"registeredBy"`
	// RegisteredAt is when the registration was made or last refreshed; ExpiresAt is
	// when the sweep removes it unless refreshed first.
	RegisteredAt time.Time `json:"registeredAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// PersonaNamed returns the cast member with the login, or nil.
func (r *Registration) PersonaNamed(login string) *Persona {
	for i := range r.Cast {
		if r.Cast[i].Login == login {
			return &r.Cast[i]
		}
	}

	return nil
}

// SignIn is one entry of the log: who signed in as which persona into which client.
type SignIn struct {
	ID          string
	Deployment  Deployment
	Application Application
	Persona     string
	Actor       Actor
	At          time.Time
}

// ClientIDFor is the client ID of an application's deployment: the root with the
// environment appended, or the pull request's number (lanterns-tst, lanterns-pr12).
func ClientIDFor(root, environment string, pullRequest int) string {
	if pullRequest > 0 {
		return root + "-pr" + strconv.Itoa(pullRequest)
	}

	return root + "-" + environment
}

// DeployerVerifier verifies the credential a deploy presents to register: a signed
// identity token whose audience is the issuer. It returns the deploy identity's email.
type DeployerVerifier interface {
	VerifyDeployer(ctx context.Context, token, audience string) (email string, err error)
}

// ActorFunc reads the signed-in person from a request under the host's session
// middleware, or reports none.
type ActorFunc func(r *http.Request) (*Actor, bool)

// Config configures the persona sign-in.
type Config struct {
	// Issuer is required: the provider's issuer URL, the host's base URL with the path
	// the handler is mounted at (https://host.example.com/persona). Applications
	// configure it as their method's issuer, and deploys present identity tokens for
	// it as their audience.
	Issuer string
	// Actor is required: how the module reads the signed-in person from a request.
	// The picker's routes are mounted under the host's session middleware.
	Actor ActorFunc
	// LoginURL is where the picker sends a browser that carries no session: the host's
	// login page, given ?returnUrl= naming the picker. (default: /login)
	LoginURL string
	// Deployers is required: it verifies the deploys' credentials. GoogleDeployers
	// verifies the identity tokens Google issues to service accounts; tests inject
	// their own.
	Deployers DeployerVerifier
	// Expiry is how long a registration stands without a refresh. (default: 7 days)
	Expiry time.Duration
	// AllowInsecureIssuer admits an http issuer, for tests and local development.
	AllowInsecureIssuer bool
	// Now is the clock. (default: time.Now)
	Now func() time.Time
}

// Errors the host and the module exchange.
var (
	// ErrUnknownApplication is the host's answer for a client ID root it never minted.
	ErrUnknownApplication = errors.New("persona: no application has this client ID")
	// ErrUnknownRegistration is the store's answer for a client ID no deploy registered.
	ErrUnknownRegistration = errors.New("persona: no deployment is registered under this client ID")
	// ErrUnknownRequest is the store's answer for a sign-in request that does not
	// exist, was used, or expired.
	ErrUnknownRequest = errors.New("persona: the sign-in request is unknown or expired")
	// ErrUnknownToken is the store's answer for an access token that does not exist or
	// expired.
	ErrUnknownToken = errors.New("persona: the access token is unknown or expired")
)
