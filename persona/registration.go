package persona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
)

// registrationPath is the registration endpoint's path under the issuer, with the
// deployment's client ID.
const registrationPath = "/registrations/{clientID}"

// maxRegistrationBody bounds a registration's JSON body.
const maxRegistrationBody = 1 << 20

// RegistrationRequest is what a deploy sends to register its deployment.
type RegistrationRequest struct {
	// ApplicationClientID is the application's client ID root, from its placement.
	ApplicationClientID string `json:"applicationClientId"`
	// Environment and PullRequest name the deployment; its client ID follows from them
	// (ClientIDFor) and must be the one in the path.
	Environment string `json:"environment"`
	PullRequest int    `json:"pullRequest"`
	// RedirectURIs are the callback addresses of every site the deployment serves.
	RedirectURIs []string `json:"redirectUris"`
	// Cast are the personas the deployment seeded.
	Cast []Persona `json:"cast"`
}

// RegistrationResponse is what the registration endpoint answers.
type RegistrationResponse struct {
	ClientID  string    `json:"clientId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// register records a deployment's registration. The deploy authenticates with a signed
// identity token for the issuer; its identity must be one the application's record
// allows to register, and the client ID in the path must be the deployment's.
func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deployer, ok := s.deployer(w, r)
	if !ok {
		return
	}
	var body RegistrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegistrationBody)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "the registration body is not the JSON expected: "+err.Error())

		return
	}
	app, status, err := s.applicationFor(ctx, body.ApplicationClientID, deployer)
	if err != nil {
		writeJSONError(w, status, errors.Cause(err).Error())

		return
	}
	clientID := chi.URLParam(r, "clientID")
	if want := ClientIDFor(app.ClientID, body.Environment, body.PullRequest); clientID != want {
		writeJSONError(w, http.StatusBadRequest, "the client ID "+clientID+" is not the deployment's: the environment and pull request name "+want)

		return
	}
	if err := s.validRegistration(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, errors.Cause(err).Error())

		return
	}
	now := s.now()
	reg := &Registration{
		Deployment:          Deployment{ClientID: clientID, Environment: body.Environment, PullRequest: body.PullRequest},
		ApplicationClientID: app.ClientID,
		RedirectURIs:        body.RedirectURIs,
		Cast:                body.Cast,
		RegisteredBy:        deployer,
		RegisteredAt:        now,
		ExpiresAt:           now.Add(s.cfg.Expiry),
	}
	if err := s.store.PutRegistration(ctx, reg); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "the registration could not be stored")

		return
	}
	writeJSON(w, http.StatusOK, RegistrationResponse{ClientID: clientID, ExpiresAt: reg.ExpiresAt})
}

// deregister removes a deployment's registration, for the deploy identity of its
// application.
func (s *Service) deregister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deployer, ok := s.deployer(w, r)
	if !ok {
		return
	}
	clientID := chi.URLParam(r, "clientID")
	reg, err := s.store.Registration(ctx, clientID)
	if err != nil {
		if errors.Is(err, ErrUnknownRegistration) {
			w.WriteHeader(http.StatusNoContent)

			return
		}
		writeJSONError(w, http.StatusInternalServerError, "the registration could not be read")

		return
	}
	if _, status, err := s.applicationFor(ctx, reg.ApplicationClientID, deployer); err != nil {
		writeJSONError(w, status, errors.Cause(err).Error())

		return
	}
	if err := s.store.DeleteRegistration(ctx, clientID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "the registration could not be removed")

		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// showRegistration answers a signed-in person with a deployment's registration, for the
// host's own pages.
func (s *Service) showRegistration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.cfg.Actor(r); !ok {
		writeJSONError(w, http.StatusUnauthorized, "sign in first")

		return
	}
	reg, err := s.store.Registration(r.Context(), chi.URLParam(r, "clientID"))
	if err != nil {
		if errors.Is(err, ErrUnknownRegistration) {
			writeJSONError(w, http.StatusNotFound, "no deployment is registered under this client ID")

			return
		}
		writeJSONError(w, http.StatusInternalServerError, "the registration could not be read")

		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// deployer verifies the bearer token a deploy presents and returns its identity, or
// writes the refusal and reports false.
func (s *Service) deployer(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="persona"`)
		writeJSONError(w, http.StatusUnauthorized, "present the deploy identity's token for the issuer as a bearer token")

		return "", false
	}
	email, err := s.cfg.Deployers.VerifyDeployer(r.Context(), token, s.cfg.Issuer)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="persona", error="invalid_token"`)
		writeJSONError(w, http.StatusUnauthorized, "the deploy identity's token was not accepted: "+err.Error())

		return "", false
	}

	return email, true
}

// applicationFor resolves the application the host minted the client ID root for and
// checks the deployer is one of its deploy identities.
func (s *Service) applicationFor(ctx context.Context, root, deployer string) (*Application, int, error) {
	if root == "" {
		return nil, http.StatusBadRequest, errors.New("applicationClientId is required: the application's client ID root")
	}
	app, err := s.host.ApplicationByClient(ctx, root)
	if err != nil {
		if errors.Is(err, ErrUnknownApplication) {
			return nil, http.StatusNotFound, errors.Newf("no application has the client ID %s", root)
		}

		return nil, http.StatusInternalServerError, errors.New("the application could not be looked up")
	}
	if !slices.ContainsFunc(app.DeployIdentities, func(id string) bool { return strings.EqualFold(id, deployer) }) {
		return nil, http.StatusForbidden, errors.Newf("%s is not a deploy identity of %s", deployer, app.Name)
	}

	return app, http.StatusOK, nil
}

// validRegistration checks a registration's shape: an environment, absolute redirect
// addresses on the scheme the issuer allows, and a cast of distinct logins.
func (s *Service) validRegistration(body *RegistrationRequest) error {
	if body.Environment == "" {
		return errors.New("environment is required")
	}
	if body.PullRequest < 0 {
		return errors.New("pullRequest must be 0 or the pull request's number")
	}
	if len(body.RedirectURIs) == 0 {
		return errors.New("redirectUris must name at least one callback address")
	}
	for _, raw := range body.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !s.cfg.AllowInsecureIssuer)) {
			return errors.Newf("redirectUris: %q is not an absolute https callback address", raw)
		}
	}
	seen := make(map[string]bool, len(body.Cast))
	for i := range body.Cast {
		p := &body.Cast[i]
		switch {
		case p.Login == "" || strings.ContainsFunc(p.Login, func(c rune) bool { return c <= ' ' || c == 0x7f }):
			return errors.Newf("cast: persona %d needs a login without spaces", i+1)
		case seen[p.Login]:
			return errors.Newf("cast: the login %q appears twice", p.Login)
		}
		seen[p.Login] = true
		if p.Name == "" {
			p.Name = p.Login
		}
	}

	return nil
}

// writeJSON writes a JSON answer.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a JSON refusal with a message. Callers pass the plain message,
// the cause of a wrapped error, not the chain with its source.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}
