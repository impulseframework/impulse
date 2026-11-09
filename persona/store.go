package persona

import (
	"context"
	"crypto/rsa"
	"time"
)

// Store keeps the module's own state between requests: the registrations, the sign-in
// requests and their codes, the access tokens the userinfo endpoint answers for, the
// signing keys, and the log. The Spanner implementation is in the spannerstore package;
// its tables are the migrations under schema/spanner/migrations.
type Store interface {
	// PutRegistration writes a registration, replacing one under the same client ID.
	PutRegistration(ctx context.Context, reg *Registration) error
	// Registration reads the registration under the client ID, ErrUnknownRegistration
	// when there is none.
	Registration(ctx context.Context, clientID string) (*Registration, error)
	// DeleteRegistration removes the registration under the client ID; a client ID
	// with none is not an error.
	DeleteRegistration(ctx context.Context, clientID string) error
	// ExpireRegistrations removes every registration whose expiry is before the time
	// and returns how many it removed.
	ExpireRegistrations(ctx context.Context, before time.Time) (int64, error)

	// CreateRequest writes a sign-in request.
	CreateRequest(ctx context.Context, req *Request) error
	// Request reads a sign-in request by ID, ErrUnknownRequest when there is none.
	Request(ctx context.Context, id string) (*Request, error)
	// RequestByCode reads the sign-in request a code was issued for, ErrUnknownRequest
	// when no request holds the code.
	RequestByCode(ctx context.Context, code string) (*Request, error)
	// CompleteRequest marks a request done: the person picked a persona, whose login is
	// the subject, at the time. ErrUnknownRequest when there is no such request.
	CompleteRequest(ctx context.Context, id, subject, actorID string, at time.Time) error
	// SaveCode records the code issued for a request. ErrUnknownRequest when there is no
	// such request.
	SaveCode(ctx context.Context, id, code string) error
	// DeleteRequest removes a request and its code; an ID with none is not an error.
	DeleteRequest(ctx context.Context, id string) error
	// ExpireRequests removes every request created before the time and returns how
	// many it removed.
	ExpireRequests(ctx context.Context, before time.Time) (int64, error)

	// CreateToken writes an access token.
	CreateToken(ctx context.Context, tok *Token) error
	// Token reads an access token by ID, ErrUnknownToken when there is none.
	Token(ctx context.Context, id string) (*Token, error)
	// ExpireTokens removes every token whose expiry is before the time and returns how
	// many it removed.
	ExpireTokens(ctx context.Context, before time.Time) (int64, error)

	// SigningKeys reads the signing keys, newest first; the first is the one that
	// signs, and the key set publishes them all.
	SigningKeys(ctx context.Context) ([]*SigningKey, error)
	// AddSigningKey adds a signing key.
	AddSigningKey(ctx context.Context, key *SigningKey) error

	// RecordSignIn appends an entry to the log.
	RecordSignIn(ctx context.Context, s *SignIn) error
}

// Request is a sign-in request as the authorize endpoint recorded it, until the code it
// issued is exchanged.
type Request struct {
	// ID identifies the request; the picker is called with it.
	ID string
	// ClientID is the deployment the browser came from.
	ClientID string
	// RedirectURI, State, Nonce, Scopes, ResponseType and ResponseMode are the
	// authorize request's parameters, as validated against the registration.
	RedirectURI  string
	State        string
	Nonce        string
	Scopes       []string
	ResponseType string
	ResponseMode string
	// CodeChallenge and CodeChallengeMethod are the PKCE challenge the code exchange
	// must answer.
	CodeChallenge       string
	CodeChallengeMethod string
	// LoginHint is the persona the application named, "" for the picker.
	LoginHint string
	// Subject is the persona's login once picked, and ActorID the person who picked it.
	Subject string
	ActorID string
	// Done reports the pick; AuthTime is when it happened.
	Done     bool
	AuthTime time.Time
	// Code is the code issued for the request, "" until issued.
	Code string
	// CreatedAt is when the request was made; requests expire.
	CreatedAt time.Time
}

// Token is an access token the code exchange issued, which the userinfo endpoint answers
// for until it expires.
type Token struct {
	ID        string
	RequestID string
	ClientID  string
	Subject   string
	Audience  []string
	Scopes    []string
	ExpiresAt time.Time
}

// SigningKey is a key the provider signs ID tokens with. Its public half is published in
// the key set under the ID.
type SigningKey struct {
	ID        string
	Key       *rsa.PrivateKey
	CreatedAt time.Time
}
