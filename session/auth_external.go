package session

import (
	"net/http"
	"net/url"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/tracer"
)

// externalLogin redirects the browser to the provider. A returnUrl that
// internalcookie.SanitizeReturnURL would change is refused with 400 rather than
// carried, so a crafted login link can't become an open redirect.
func (a *Auth[S, U, Tx]) externalLogin(m *signin.Method) http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		returnURL := r.URL.Query().Get("returnUrl")
		if returnURL != "" && internalcookie.SanitizeReturnURL(returnURL) != returnURL {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessage("returnUrl must be a path in this application"))
		}

		providerURL, err := m.Begin(ctx, w, r, returnURL)
		if err != nil {
			if httpio.HasBadRequest(err) {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			sessioncore.RedirectRefusedLogin(w, r, m.LoginURL(), err)

			return errors.Wrap(err, string(m.Name)+": start the sign-in")
		}

		// The target is the provider's authorization URL, built from server-side
		// configuration; the caller's returnUrl only rides in the state cookie.
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
		http.Redirect(w, r, providerURL, http.StatusFound)

		return nil
	})
}

// onlyGoogleVouches takes from identity the word of every method but Google's that the
// address is verified (Identity.EmailVerified). The people who run an identity provider
// can mark any address verified, so a provider's word confirms nothing: the address is
// written on no account by it and finds no account by it, and the person's first
// sign-in to an account with no confirmed address waits for the mailed code. It holds
// for every method whatever it fills in, the library's own and one an application
// registers. Google's method keeps its word, which it gives only for an address Google
// is the authority for: a Gmail address and a Workspace account's. Google's method is
// the one that carries the library's mark (signin.Method.AddressAuthority), which only
// session/google sets and no application's method can carry: a method that takes
// Google's name is not Google's, and its word is taken from it like any other's.
func onlyGoogleVouches(m *signin.Method, identity *sessioninfo.Identity) {
	if identity == nil {
		return
	}
	if !m.AddressAuthority.IsGoogle() || identity.Method != sessioninfo.MethodGoogle {
		identity.EmailVerified = false
	}
}

// externalCallback completes the provider round trip of the registration named name.
// See the README's "The redirect and response contract" for where it redirects.
func (a *Auth[S, U, Tx]) externalCallback(name string, issuers signin.Issuers, m *signin.Method) http.HandlerFunc {
	return a.baseSession.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		v, err := m.Complete(ctx, w, r)
		if err != nil {
			sessioncore.RedirectRefusedLogin(w, r, m.LoginURL(), err)

			return errors.Wrap(err, string(m.Name)+": complete the sign-in")
		}
		onlyGoogleVouches(m, v.Identity)

		// The provider's session id is recorded under the issuer of the token that
		// arrived, since it is unique only there. A registration that declares its
		// issuers accepts a round trip from one of them and no other; one that declares
		// none records no provider session id.
		providerSessionID, providerSessionIssuer := v.ProviderSessionID, v.Issuer
		switch {
		case issuers == nil:
			providerSessionIssuer = ""
		case !issuers.Contains(v.Issuer):
			refusal := sessioninfo.NewLoginRefusal(sessioninfo.RefusedInternalError,
				httpio.NewInternalServerErrorMessagef("the %s sign-in verified a token from %q, an issuer its registration does not declare", m.Name, v.Issuer))
			sessioncore.RedirectRefusedLogin(w, r, m.LoginURL(), refusal)

			return refusal
		}
		if providerSessionID == "" || providerSessionIssuer == "" {
			providerSessionID, providerSessionIssuer = "", ""
		}
		outcome, err := a.signIn(ctx, w, &signInAttempt{
			identity:              v.Identity,
			reason:                sessioninfo.ReasonLogin,
			registration:          name,
			returnURL:             v.ReturnURL,
			roleNames:             v.RoleNames,
			providerSessionID:     providerSessionID,
			providerSessionIssuer: providerSessionIssuer,
			sameSite:              sessioncore.SameSiteNone,
		})
		if err != nil {
			sessioncore.RedirectRefusedLogin(w, r, m.LoginURL(), err)

			return err
		}
		if outcome.pending != nil {
			target, handled, err := a.onPending(ctx, w, r, outcome.pending)
			switch {
			case handled:
				return err
			case err != nil:
				sessioncore.RedirectRefusedLogin(w, r, m.LoginURL(), err)

				return err
			case target == "":
				target = pendingRedirectURL(m.LoginURL(), outcome.pending.Step, v.ReturnURL)
			}
			// The target is the configured login URL or the PendingHook's choice. It is
			// written as is, never cleaned.
			internalcookie.WriteRedirect(w, target, http.StatusFound)

			return nil
		}

		// The method already sanitized returnURL. It is sanitized again so the
		// Location header is exactly a value SanitizeReturnURL keeps.
		internalcookie.RedirectToReturnURL(w, v.ReturnURL, http.StatusFound)

		return nil
	})
}

// pendingRedirectURL returns where an external sign-in that became a pending identity
// sends the browser: the login URL with pending=<reason>, plus the sanitized returnUrl
// when it is not the root.
func pendingRedirectURL(loginURL string, reason sessioninfo.PendingReason, returnURL string) string {
	returnURL = internalcookie.SanitizeReturnURL(returnURL)
	u, err := url.Parse(loginURL)
	if err != nil {
		// An unparsable login URL still gets the reason, as a refusal gets its code.
		u = &url.URL{Path: loginURL}
	}
	q := u.Query()
	q.Set("pending", string(reason))
	if returnURL != "/" {
		q.Set("returnUrl", returnURL)
	}
	u.RawQuery = q.Encode()

	return u.String()
}
