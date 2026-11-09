package persona

import (
	"context"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-playground/errors/v5"
	"github.com/gofrs/uuid/v5"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const (
	// requestLifetime is how long a sign-in request stands, from the authorize call to
	// the code exchange.
	requestLifetime = 10 * time.Minute
	// tokenLifetime is how long an access token answers at the userinfo endpoint; an
	// application reads the ID token and is done with it.
	tokenLifetime = 5 * time.Minute
	// idTokenLifetime is the ID token's validity. The application verifies it on the
	// callback, moments after it is issued.
	idTokenLifetime = 10 * time.Minute
)

var (
	_ op.Storage                   = &storage{}
	_ op.CanSetUserinfoFromRequest = &storage{}
	_ op.Client                    = &client{}
	_ op.AuthRequest               = &authRequest{}
)

// storage is the provider library's storage over the service: the registrations are
// its clients, the sign-in requests its authorize requests, the personas its users.
type storage struct {
	svc *Service
}

// CreateAuthRequest records an authorize request the library validated against the
// client. A request that forbids interaction (prompt=none) cannot pick a persona and is
// refused.
func (s *storage) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, _ string) (op.AuthRequest, error) {
	for _, prompt := range authReq.Prompt {
		if prompt == oidc.PromptNone {
			return nil, oidc.ErrLoginRequired()
		}
	}
	id, err := uuid.NewV4()
	if err != nil {
		return nil, errors.Wrap(err, "uuid.NewV4()")
	}
	req := &Request{
		ID: id.String(), ClientID: authReq.ClientID, RedirectURI: authReq.RedirectURI, State: authReq.State, Nonce: authReq.Nonce, Scopes: authReq.Scopes,
		ResponseType: string(authReq.ResponseType), ResponseMode: string(authReq.ResponseMode), CodeChallenge: authReq.CodeChallenge,
		CodeChallengeMethod: string(authReq.CodeChallengeMethod), LoginHint: authReq.LoginHint, CreatedAt: s.svc.now(),
	}
	if err := s.svc.store.CreateRequest(ctx, req); err != nil {
		return nil, serverError("the sign-in request could not be stored", errors.Wrap(err, "persona.Store.CreateRequest()"))
	}

	return &authRequest{req}, nil
}

// AuthRequestByID reads a live sign-in request.
func (s *storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	req, err := s.svc.liveRequest(ctx, id)
	if err != nil {
		if errors.Is(err, ErrUnknownRequest) {
			return nil, oidc.ErrInvalidRequest().WithDescription("the sign-in request is unknown or has expired; start the sign-in again").WithParent(err)
		}

		return nil, serverError("the sign-in request could not be read", err)
	}

	return &authRequest{req}, nil
}

// AuthRequestByCode reads the live sign-in request a code was issued for.
func (s *storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	req, err := s.svc.store.RequestByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrUnknownRequest) {
			return nil, ErrUnknownRequest
		}

		return nil, serverError("the sign-in request could not be read", errors.Wrap(err, "persona.Store.RequestByCode()"))
	}
	if s.svc.expired(req) {
		return nil, ErrUnknownRequest
	}

	return &authRequest{req}, nil
}

// SaveAuthCode records the code issued for a request.
func (s *storage) SaveAuthCode(ctx context.Context, id, code string) error {
	if err := s.svc.store.SaveCode(ctx, id, code); err != nil {
		return serverError("the sign-in's code could not be stored", errors.Wrap(err, "persona.Store.SaveCode()"))
	}

	return nil
}

// DeleteAuthRequest removes a request once its code is exchanged.
func (s *storage) DeleteAuthRequest(ctx context.Context, id string) error {
	if err := s.svc.store.DeleteRequest(ctx, id); err != nil {
		return serverError("the sign-in request could not be removed", errors.Wrap(err, "persona.Store.DeleteRequest()"))
	}

	return nil
}

// CreateAccessToken issues the access token of a code exchange: an opaque token the
// userinfo endpoint answers for until it expires.
func (s *storage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (token string, expiresAt time.Time, err error) {
	req, ok := request.(*authRequest)
	if !ok {
		return "", time.Time{}, oidc.ErrUnsupportedGrantType().WithDescription("the persona sign-in issues tokens for the authorization code alone")
	}
	id, err := uuid.NewV4()
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, "uuid.NewV4()")
	}
	tok := &Token{ID: id.String(), RequestID: req.ID, ClientID: req.ClientID, Subject: req.Subject, Audience: req.GetAudience(), Scopes: req.Scopes, ExpiresAt: s.svc.now().Add(tokenLifetime)}
	if err := s.svc.store.CreateToken(ctx, tok); err != nil {
		return "", time.Time{}, serverError("the access token could not be stored", errors.Wrap(err, "persona.Store.CreateToken()"))
	}

	return tok.ID, tok.ExpiresAt, nil
}

// CreateAccessAndRefreshTokens is not offered: no client registers the refresh grant.
func (*storage) CreateAccessAndRefreshTokens(context.Context, op.TokenRequest, string) (accessToken, refreshToken string, expiresAt time.Time, err error) {
	return "", "", time.Time{}, oidc.ErrUnsupportedGrantType().WithDescription("the persona sign-in issues no refresh tokens")
}

// TokenRequestByRefreshToken is not offered.
func (*storage) TokenRequestByRefreshToken(context.Context, string) (op.RefreshTokenRequest, error) {
	return nil, op.ErrInvalidRefreshToken
}

// TerminateSession has nothing to end: the provider keeps no session of its own.
func (*storage) TerminateSession(context.Context, string, string) error {
	return nil
}

// RevokeToken has nothing to revoke beyond expiry.
func (*storage) RevokeToken(context.Context, string, string, string) *oidc.Error {
	return nil
}

// GetRefreshTokenInfo is not offered.
func (*storage) GetRefreshTokenInfo(context.Context, string, string) (userID, tokenID string, err error) {
	return "", "", op.ErrInvalidRefreshToken
}

// SigningKey is the key that signs: the newest.
func (s *storage) SigningKey(ctx context.Context) (op.SigningKey, error) {
	list, err := s.svc.signingKeys(ctx)
	if err != nil {
		return nil, serverError("the signing keys could not be read", err)
	}

	return &signingKey{list[0]}, nil
}

// SignatureAlgorithms is the one algorithm the provider signs with.
func (*storage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.RS256}, nil
}

// KeySet publishes every signing key's public half, so a token signed before a rotation
// still verifies.
func (s *storage) KeySet(ctx context.Context) ([]op.Key, error) {
	list, err := s.svc.signingKeys(ctx)
	if err != nil {
		return nil, serverError("the signing keys could not be read", err)
	}
	set := make([]op.Key, 0, len(list))
	for _, k := range list {
		set = append(set, &publicKey{k})
	}

	return set, nil
}

// GetClientByClientID is the registration under the client ID, as a client.
func (s *storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	reg, err := s.svc.store.Registration(ctx, clientID)
	if err != nil {
		if errors.Is(err, ErrUnknownRegistration) {
			return nil, oidc.ErrInvalidClient().WithDescription("no deployment is registered under this client ID")
		}

		return nil, serverError("the deployment's registration could not be read", errors.Wrap(err, "persona.Store.Registration()"))
	}
	if s.svc.now().After(reg.ExpiresAt) {
		return nil, oidc.ErrInvalidClient().WithDescription("the deployment's registration has expired")
	}

	return &client{reg: reg, svc: s.svc}, nil
}

// AuthorizeClientIDSecret is never satisfied: every client is public.
func (*storage) AuthorizeClientIDSecret(context.Context, string, string) error {
	return oidc.ErrInvalidClient().WithDescription("the persona sign-in's clients are public and hold no secret")
}

// SetUserinfoFromScopes is the deprecated hook; SetUserinfoFromRequest fills the claims.
func (*storage) SetUserinfoFromScopes(context.Context, *oidc.UserInfo, string, string, []string) error {
	return nil
}

// SetUserinfoFromRequest fills the ID token's user claims from the persona the request
// signed in: the login as the preferred username, the name and the address.
func (s *storage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, request op.IDTokenRequest, scopes []string) error {
	return s.setUserinfo(ctx, userinfo, request.GetClientID(), request.GetSubject(), scopes)
}

// SetUserinfoFromToken answers the userinfo endpoint for a live access token.
func (s *storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, _, _ string) error {
	tok, err := s.svc.store.Token(ctx, tokenID)
	if err != nil {
		if errors.Is(err, ErrUnknownToken) {
			return ErrUnknownToken
		}

		return serverError("the access token could not be read", errors.Wrap(err, "persona.Store.Token()"))
	}
	if s.svc.now().After(tok.ExpiresAt) {
		return ErrUnknownToken
	}

	return s.setUserinfo(ctx, userinfo, tok.ClientID, tok.Subject, tok.Scopes)
}

// setUserinfo fills the user claims the scopes ask for from the persona.
func (s *storage) setUserinfo(ctx context.Context, userinfo *oidc.UserInfo, clientID, subject string, scopes []string) error {
	reg, err := s.svc.store.Registration(ctx, clientID)
	if err != nil {
		if errors.Is(err, ErrUnknownRegistration) {
			return oidc.ErrInvalidClient().WithDescription("the deployment's registration is gone; deploy it again")
		}

		return serverError("the deployment's registration could not be read", errors.Wrap(err, "persona.Store.Registration()"))
	}
	p := reg.PersonaNamed(subject)
	if p == nil {
		return oidc.ErrServerError().WithDescription("the persona is no longer in the deployment's cast; deploy it again")
	}
	userinfo.Subject = p.Login
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeProfile:
			userinfo.Name = p.Name
			userinfo.PreferredUsername = p.Login
		case oidc.ScopeEmail:
			userinfo.Email = p.Email
			userinfo.EmailVerified = oidc.Bool(p.Email != "")
		}
	}

	return nil
}

// SetIntrospectionFromToken is not offered: introspection needs a client that can
// authenticate, and none can.
func (*storage) SetIntrospectionFromToken(context.Context, *oidc.IntrospectionResponse, string, string, string) error {
	return oidc.ErrInvalidClient().WithDescription("the persona sign-in offers no introspection")
}

// GetPrivateClaimsFromScopes adds no private claims: the provider asserts identity alone,
// and the application's seed decides what a persona may do.
func (*storage) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, nil
}

// GetKeyByIDAndClientID is not offered: no client authenticates with a key.
func (*storage) GetKeyByIDAndClientID(context.Context, string, string) (*jose.JSONWebKey, error) {
	return nil, oidc.ErrInvalidClient().WithDescription("the persona sign-in's clients hold no keys")
}

// ValidateJWTProfileScopes is not offered.
func (*storage) ValidateJWTProfileScopes(context.Context, string, []string) ([]string, error) {
	return nil, oidc.ErrUnsupportedGrantType().WithDescription("the persona sign-in offers no JWT profile grant")
}

// Health reports the store reachable.
func (*storage) Health(context.Context) error {
	return nil
}

// serverError is an internal failure as the provider reports it: a plain description
// for the application and the browser, the wrapped cause for the log.
func serverError(description string, err error) *oidc.Error {
	return oidc.ErrServerError().WithDescription("%s", description).WithParent(err)
}

// client is a registration as the provider library sees a client: a public client of the
// code flow, sent to the picker to sign in, whose redirect addresses are the registered
// ones.
type client struct {
	reg *Registration
	svc *Service
}

func (c *client) GetID() string {
	return c.reg.ClientID
}

func (c *client) RedirectURIs() []string {
	return c.reg.RedirectURIs
}

func (*client) PostLogoutRedirectURIs() []string {
	return nil
}

func (*client) ApplicationType() op.ApplicationType {
	return op.ApplicationTypeWeb
}

func (*client) AuthMethod() oidc.AuthMethod {
	return oidc.AuthMethodNone
}

func (*client) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}

func (*client) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode}
}

// LoginURL is the picker for the request.
func (c *client) LoginURL(requestID string) string {
	return c.svc.pickerURL(requestID)
}

func (*client) AccessTokenType() op.AccessTokenType {
	return op.AccessTokenTypeBearer
}

func (*client) IDTokenLifetime() time.Duration {
	return idTokenLifetime
}

// DevMode admits http redirect addresses where the issuer itself is admitted insecure.
func (c *client) DevMode() bool {
	return c.svc.cfg.AllowInsecureIssuer
}

func (*client) RestrictAdditionalIdTokenScopes() func(scopes []string) []string {
	return func(scopes []string) []string {
		return scopes
	}
}

func (*client) RestrictAdditionalAccessTokenScopes() func(scopes []string) []string {
	return func(scopes []string) []string {
		return scopes
	}
}

// IsScopeAllowed admits no scope beyond the standard ones.
func (*client) IsScopeAllowed(string) bool {
	return false
}

// IDTokenUserinfoClaimsAssertion puts the user claims in the ID token itself, which is
// what the application reads; it never calls the userinfo endpoint.
func (*client) IDTokenUserinfoClaimsAssertion() bool {
	return true
}

func (*client) ClockSkew() time.Duration {
	return 0
}

// authRequest is a sign-in request as the provider library reads it.
type authRequest struct {
	*Request
}

func (a *authRequest) GetID() string {
	return a.ID
}

func (*authRequest) GetACR() string {
	return ""
}

// GetAMR asserts no authentication method: the host authenticated the person, and the
// application's registration declares the provider as its proof.
func (*authRequest) GetAMR() []string {
	return nil
}

func (a *authRequest) GetAudience() []string {
	return []string{a.ClientID}
}

func (a *authRequest) GetAuthTime() time.Time {
	return a.AuthTime
}

func (a *authRequest) GetClientID() string {
	return a.ClientID
}

func (a *authRequest) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" {
		return nil
	}

	return &oidc.CodeChallenge{Challenge: a.CodeChallenge, Method: oidc.CodeChallengeMethod(a.CodeChallengeMethod)}
}

func (a *authRequest) GetNonce() string {
	return a.Nonce
}

func (a *authRequest) GetRedirectURI() string {
	return a.RedirectURI
}

func (a *authRequest) GetResponseType() oidc.ResponseType {
	return oidc.ResponseType(a.ResponseType)
}

func (a *authRequest) GetResponseMode() oidc.ResponseMode {
	return oidc.ResponseMode(a.ResponseMode)
}

func (a *authRequest) GetScopes() []string {
	return a.Scopes
}

func (a *authRequest) GetState() string {
	return a.State
}

func (a *authRequest) GetSubject() string {
	return a.Subject
}

func (a *authRequest) Done() bool {
	return a.Request.Done
}

// signingKey is a key as the library signs with it.
type signingKey struct {
	*SigningKey
}

func (*signingKey) SignatureAlgorithm() jose.SignatureAlgorithm {
	return jose.RS256
}

func (k *signingKey) Key() any {
	return k.SigningKey.Key
}

func (k *signingKey) ID() string {
	return k.SigningKey.ID
}

// publicKey is a key's public half as the key set publishes it.
type publicKey struct {
	*SigningKey
}

func (k *publicKey) ID() string {
	return k.SigningKey.ID
}

func (*publicKey) Algorithm() jose.SignatureAlgorithm {
	return jose.RS256
}

func (*publicKey) Use() string {
	return "sig"
}

func (k *publicKey) Key() any {
	return &k.SigningKey.Key.PublicKey
}
