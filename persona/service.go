package persona

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/gofrs/uuid/v5"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"
)

const (
	defaultLoginURL = "/login"
	defaultExpiry   = 7 * 24 * time.Hour
	// keyCacheTTL is how long the signing keys are kept in memory before the store is
	// asked again, so a rotation reaches every instance within it.
	keyCacheTTL = time.Minute
	// rsaKeyBits is the size of a signing key the service generates.
	rsaKeyBits = 2048
)

// Service is the persona sign-in: the provider, the picker, the registration endpoint
// and the sweep, over a host and a store.
type Service struct {
	host     Host
	store    Store
	cfg      Config
	issuer   *url.URL
	provider *op.Provider
	callback func(ctx context.Context, requestID string) string

	keysMu      sync.Mutex
	keys        []*SigningKey
	keysReadAt  time.Time
	cryptoKeyID string
}

// New builds the service. It refuses a configuration without an issuer, an actor reader or
// a deployer verifier, and an http issuer unless AllowInsecureIssuer says so. It makes the
// first signing key when the store holds none.
func New(ctx context.Context, host Host, store Store, config *Config) (*Service, error) {
	if host == nil || store == nil || config == nil {
		return nil, errors.New("persona.New: a host, a store and a configuration are required")
	}
	cfg := *config
	issuer, err := url.Parse(cfg.Issuer)
	switch {
	case cfg.Issuer == "":
		return nil, errors.New("persona.New: Config.Issuer is required: the provider's URL, the host's base URL with the handler's mount path")
	case err != nil:
		return nil, errors.Wrapf(err, "persona.New: Config.Issuer %q is not a URL", cfg.Issuer)
	case issuer.Host == "" || (issuer.Scheme != "https" && (issuer.Scheme != "http" || !cfg.AllowInsecureIssuer)) || issuer.RawQuery != "" || issuer.Fragment != "":
		return nil, errors.Newf("persona.New: Config.Issuer %q must be an absolute https URL (http with AllowInsecureIssuer) without a query or fragment", cfg.Issuer)
	case cfg.Actor == nil:
		return nil, errors.New("persona.New: Config.Actor is required: how the signed-in person is read from a request")
	case cfg.Deployers == nil:
		return nil, errors.New("persona.New: Config.Deployers is required: GoogleDeployers in production")
	}
	cfg.Issuer = strings.TrimSuffix(cfg.Issuer, "/")
	if cfg.LoginURL == "" {
		cfg.LoginURL = defaultLoginURL
	}
	if cfg.Expiry <= 0 {
		cfg.Expiry = defaultExpiry
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Service{host: host, store: store, cfg: cfg, issuer: issuer}

	keys, err := s.signingKeys(ctx)
	if err != nil {
		return nil, err
	}
	// The access token is the token's ID encrypted under a key every instance shares:
	// one derived from the signing key, which the store holds for all of them.
	der, err := x509.MarshalPKCS8PrivateKey(keys[0].Key)
	if err != nil {
		return nil, errors.Wrap(err, "x509.MarshalPKCS8PrivateKey()")
	}
	opConfig := &op.Config{
		CryptoKey:          sha256.Sum256(der),
		CryptoKeyId:        keys[0].ID,
		CodeMethodS256:     true,
		SupportedScopes:    []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail},
		SupportedClaims:    []string{"sub", "aud", "exp", "iat", "iss", "auth_time", "nonce", "name", "preferred_username", "email", "email_verified"},
		SupportedUILocales: []language.Tag{language.English},
	}
	s.cryptoKeyID = keys[0].ID
	opts := []op.Option{}
	if cfg.AllowInsecureIssuer {
		opts = append(opts, op.WithAllowInsecure())
	}
	provider, err := op.NewProvider(opConfig, &storage{svc: s}, op.StaticIssuer(cfg.Issuer), opts...)
	if err != nil {
		return nil, errors.Wrap(err, "op.NewProvider()")
	}
	s.provider = provider
	s.callback = op.AuthCallbackURL(provider)

	return s, nil
}

// Handler serves everything under the issuer's path: the provider's endpoints (the
// discovery document, authorize, token, keys, userinfo), the picker, and the
// registration endpoint. The host mounts it at the issuer's path, under the middleware
// that starts its session, which the picker reads the signed-in person from.
func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Get(pickerPath+"/{request}", s.pickerPage)
	r.Post(pickerPath+"/{request}", s.pick)
	r.Put(registrationPath, s.register)
	r.Delete(registrationPath, s.deregister)
	r.Get(registrationPath, s.showRegistration)
	r.Mount("/", s.provider)

	return r
}

// Sweep removes what has expired: registrations past their expiry, sign-in requests
// older than their lifetime, and access tokens past theirs. It returns how many rows
// went. The host runs it on a schedule.
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	now := s.now()
	var total int64
	for _, step := range []struct {
		name string
		run  func(context.Context, time.Time) (int64, error)
		at   time.Time
	}{
		{"persona.Store.ExpireRegistrations()", s.store.ExpireRegistrations, now},
		{"persona.Store.ExpireRequests()", s.store.ExpireRequests, now.Add(-requestLifetime)},
		{"persona.Store.ExpireTokens()", s.store.ExpireTokens, now},
	} {
		n, err := step.run(ctx, step.at)
		if err != nil {
			return total, errors.Wrap(err, step.name)
		}
		total += n
	}

	return total, nil
}

// now is the service's clock.
func (s *Service) now() time.Time {
	return s.cfg.Now()
}

// pickerURL is the picker's absolute URL for a request: where the provider sends the
// browser after it recorded the authorize request.
func (s *Service) pickerURL(requestID string) string {
	return s.cfg.Issuer + pickerPath + "/" + url.PathEscape(requestID)
}

// callbackURL is where the provider finishes a sign-in request: its callback with the
// request's ID. The provider derives it from the issuer in the context, which its own
// handlers carry and the picker's do not, so the issuer is set here.
func (s *Service) callbackURL(ctx context.Context, requestID string) string {
	return s.callback(op.ContextWithIssuer(ctx, s.cfg.Issuer), requestID)
}

// liveRequest reads a sign-in request that has not expired.
func (s *Service) liveRequest(ctx context.Context, id string) (*Request, error) {
	req, err := s.store.Request(ctx, id)
	if err != nil {
		if errors.Is(err, ErrUnknownRequest) {
			return nil, ErrUnknownRequest
		}

		return nil, errors.Wrap(err, "persona.Store.Request()")
	}
	if s.expired(req) {
		return nil, ErrUnknownRequest
	}

	return req, nil
}

// expired reports a request older than its lifetime.
func (s *Service) expired(req *Request) bool {
	return s.now().After(req.CreatedAt.Add(requestLifetime))
}

// signingKeys returns the signing keys, newest first, from memory when read within the
// cache's lifetime, making the first one when the store holds none.
func (s *Service) signingKeys(ctx context.Context) ([]*SigningKey, error) {
	s.keysMu.Lock()
	defer s.keysMu.Unlock()
	if len(s.keys) > 0 && s.now().Before(s.keysReadAt.Add(keyCacheTTL)) {
		return s.keys, nil
	}
	keys, err := s.store.SigningKeys(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "persona.Store.SigningKeys()")
	}
	if len(keys) == 0 {
		key, err := s.generateKey()
		if err != nil {
			return nil, err
		}
		if err := s.store.AddSigningKey(ctx, key); err != nil {
			return nil, errors.Wrap(err, "persona.Store.AddSigningKey()")
		}
		// Another instance may have added one in the same moment; the store's order
		// decides which signs, so read back rather than trust ours.
		if keys, err = s.store.SigningKeys(ctx); err != nil {
			return nil, errors.Wrap(err, "persona.Store.SigningKeys()")
		}
	}
	s.keys, s.keysReadAt = keys, s.now()

	return keys, nil
}

// generateKey makes a signing key.
func (s *Service) generateKey() (*SigningKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, errors.Wrap(err, "rsa.GenerateKey()")
	}
	id, err := uuid.NewV4()
	if err != nil {
		return nil, errors.Wrap(err, "uuid.NewV4()")
	}

	return &SigningKey{ID: id.String(), Key: key, CreatedAt: s.now()}, nil
}

// RotateSigningKey adds a new signing key, which signs from the next cache refresh on;
// the key set keeps publishing the older ones.
func (s *Service) RotateSigningKey(ctx context.Context) error {
	key, err := s.generateKey()
	if err != nil {
		return err
	}
	if err := s.store.AddSigningKey(ctx, key); err != nil {
		return errors.Wrap(err, "persona.Store.AddSigningKey()")
	}
	s.keysMu.Lock()
	s.keys, s.keysReadAt = nil, time.Time{}
	s.keysMu.Unlock()

	return nil
}
