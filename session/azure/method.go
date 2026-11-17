package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/oidc"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// Config configures the Microsoft Entra ID (Azure) sign-in method.
type Config struct {
	// Tenants is required: which Microsoft Entra tenants' accounts may sign in. One
	// tenant, Tenant(id) or NamedTenant(id, name), is the application that belongs to
	// one organization, served at the tenant's own endpoint; Tenants(...) lists
	// several; AnyTenant() admits the work or school account of any tenant, and
	// PersonalAccounts() Microsoft personal accounts. The verified tid claim selects
	// the entry at the callback, before the account rules run, so an account the slot
	// does not admit never reaches a row (unlisted_tenant, personal_account); under
	// every shape but one tenant the token's issuer is checked against its tenant by
	// this library, since Microsoft's shared endpoints name no one issuer.
	Tenants TenantSet
	// Authority is the base URL of the Microsoft identity platform the tenants live
	// on: DefaultAuthority, https://login.microsoftonline.com, when empty;
	// https://login.microsoftonline.us for Azure Government. Issuers are
	// <Authority>/<tenant id>/v2.0.
	Authority string
	// ClientID and ClientSecret are the application registration's.
	ClientID     string
	ClientSecret string
	// RedirectURL is where Microsoft sends the browser back: the Callback route.
	RedirectURL string
	// RoleSync is required: signin.RoleSync(manager) reconciles the user's roles to
	// the token's roles claim whenever an Azure sign-in establishes a session, and
	// refuses the sign-in (no_roles) when no recognized role results;
	// signin.DisableRoleSync() leaves roles to the application. Role synchronization
	// follows one tenant's application roles: it is refused with several tenants, any
	// tenant or personal accounts.
	RoleSync signin.RoleSyncConfig
	// LoginURL is where refused and pending sign-ins are sent. (default: /login)
	LoginURL string
	// Authenticator replaces the verifier built from the fields above. Tests inject a
	// fake identity provider through it; production leaves it nil.
	Authenticator Authenticator
}

// Identity is a verified Microsoft Entra ID identity as the account rule receives it.
type Identity struct {
	// TenantID and ObjectID key the identity: the tid and oid claims.
	TenantID string
	ObjectID string
	// Organization is the tenant as the registration's Tenants slot names it: the
	// application's name for a named tenant, the tenant id otherwise. The standard
	// account rules store it as the tenant key of the link they make.
	Organization string
	// Email is the email claim. Microsoft does not verify it.
	Email string
	// PreferredUsername and Name are the preferred_username and name claims. The
	// preferred username is the user principal name of a member of the tenant, which
	// the standard account rule uses as the username; a guest's is an address the
	// tenant did not verify.
	PreferredUsername string
	Name              string
	// Guest reports that the account is a guest of the tenant (B2B), not a member: the
	// idp claim names another identity provider than the token's issuer, or the
	// preferred username carries the #EXT# marker of a guest's user principal name.
	Guest bool
	// Roles are the application roles the token asserts (the roles claim).
	Roles []string
	// AMR are the authentication methods the token asserts (the amr claim).
	AMR []string
	// SessionID is the sid claim: Entra's session id, which its front-channel logout
	// names.
	SessionID string
	// Claims are the ID token's claims as issued, for anything the fields above do not
	// carry.
	Claims json.RawMessage
}

// AccountRule is the application's own rule for an Entra ID identity that is not
// linked to any user yet, given to SignIn; nil leaves every such identity to the
// standard rule, which reads what an administrator chose for the registration in the
// running application. See signin.AccountRule for when it runs and what it
// may do. Tx is the Auth's storage transaction type.
type AccountRule[Tx any] func(ctx context.Context, tx Tx, identity *Identity) (*signin.Resolution, error)

// Method holds the handlers of the Microsoft Entra ID (Azure) sign-in method.
type Method struct {
	login              http.HandlerFunc
	callback           http.HandlerFunc
	frontChannelLogout http.HandlerFunc
}

// SignIn registers Microsoft Entra ID (Azure) OpenID Connect sign-in on auth under
// name and returns its handlers. name identifies the registration on auth, so one Auth
// can offer several registrations of the method. The identity key is (tid, oid) under
// every shape of cfg.Tenants. An identity that is not linked yet is decided by rule
// when one is given and by the library's standard rule otherwise, or when rule answers
// signin.Standard; nil is the usual value. The standard rule admits a person with no
// account by invitation only, until an administrator opens the registration to sign-up
// in the running application: how wide the registration is (cfg.Tenants) is whom that
// opens it to. A guest of the tenant is refused by the standard rule (guest_account).
//
// It panics when name is empty or already used on auth, when auth has served a
// request, when cfg.RoleSync or cfg.Tenants is missing or malformed, when
// cfg.Authority is not a base URL, and when role synchronization is asked for with
// other than one tenant.
func SignIn[Tx any](auth signin.Core[Tx], name string, rule AccountRule[Tx], cfg *Config) *Method {
	if cfg == nil {
		cfg = &Config{}
	}
	authority, misuse := authorityOf(cfg.Authority)
	if misuse == nil {
		misuse = refuseShape(cfg.Tenants, cfg.RoleSync)
	}
	reg := &signin.Registration[Tx]{
		Name:               name,
		Method:             sessioninfo.MethodAzure,
		AccountRule:        accountRule(rule),
		FrontChannelLogout: true,
		Issuers:            cfg.Tenants.issuers(authority),
		Err:                misuse,
		Build: func(cookies signin.StateCookies) (*signin.Method, error) {
			roles, err := signin.ReconcilerOf(cfg.RoleSync)
			if err != nil {
				return nil, errors.Wrap(err, "signin.ReconcilerOf()")
			}
			authn := cfg.Authenticator
			if authn == nil {
				o := NewOIDC(cookies, authority, cfg.Tenants, cfg.ClientID, cfg.ClientSecret, cfg.RedirectURL)
				o.simulateAs(name)
				if cfg.LoginURL != "" {
					o.SetLoginURL(cfg.LoginURL)
				}
				authn = o
			}

			return method(authn, roles, authority, cfg.Tenants), nil
		},
	}
	h := signin.Register(auth, reg)

	return &Method{login: h.Login, callback: h.Callback, frontChannelLogout: h.FrontChannelLogout}
}

// refuseShape is the registration's startup check of the organization slot against the
// role sync slot, each refusal naming its field: the slot's own rules (validate), and
// role synchronization with other than one tenant.
func refuseShape(tenants TenantSet, roleSync signin.RoleSyncConfig) error {
	if err := tenants.validate(); err != nil {
		return err
	}
	roles, err := signin.ReconcilerOf(roleSync)
	if err != nil {
		return errors.Wrap(err, "signin.ReconcilerOf()")
	}
	if _, one := tenants.one(); !one && roles != nil {
		return errors.New("RoleSync: role synchronization follows one tenant's application roles; a registration serving several tenants, any tenant or personal accounts takes signin.DisableRoleSync()")
	}

	return nil
}

// Login redirects to Microsoft. It accepts ?returnUrl=, which must be a path in this
// application; any other value is refused with 400. It also accepts
// ?prompt=select_account, passed to Microsoft as its prompt so it shows its account
// chooser even when it holds one signed-in account it would otherwise take without
// asking: a login page sends it after a refusal, so a person refused with the wrong
// account can pick another instead of being sent round the same loop, and under "Use
// a different account". Any other prompt value is refused with 400.
func (m *Method) Login() http.HandlerFunc { return m.login }

// Callback completes the provider round trip, resolves the identity and applies the
// sign-in policy. It then redirects:
//   - to returnUrl (default "/") when the session is established;
//   - to <LoginURL>?pending=<reason>[&returnUrl=<path>], or where a PendingHook sends
//     it, when the sign-in waits for confirmation or MFA;
//   - to <LoginURL>?code=<LoginRefusalCode> when it is refused.
func (m *Method) Callback() http.HandlerFunc { return m.callback }

// FrontChannelLogout handles Microsoft-initiated logout. It ends every session whose
// sign-in carried the sid the request names (?sid=) under an issuer the registration
// admits, answers 200, and 400 without a sid. Entra sends the sid alone: a registration
// serving one tenant scopes the request to that tenant's issuer, and one serving
// several to whichever admitted issuers live sessions carry the sid under. A request
// that does name an issuer (?iss=) is scoped to it, and refused with 400 when no tenant
// the registration admits has it. Register its URL as the application's front-channel
// logout URL in Entra. Route it without ValidateSession: Entra calls it in a frame,
// with no session of its own, and a known sid under an admitted issuer is the request's
// only credential, as the front-channel logout design has it. An app registration takes
// one such URL, and the handler reaches the sessions of its own Auth: an application
// whose several Auths should all end at Microsoft's sign-out registers one Entra
// application per Auth.
func (m *Method) FrontChannelLogout() http.HandlerFunc { return m.frontChannelLogout }

// claims are the ID token's claims the method reads.
type claims struct {
	Iss               string   `json:"iss"`
	Idp               string   `json:"idp"`
	Tid               string   `json:"tid"`
	Oid               string   `json:"oid"`
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Roles             []string `json:"roles"`
	Amr               []string `json:"amr"`
	Sid               string   `json:"sid"`
}

// guestMarker is the marker Entra puts in a guest's user principal name.
const guestMarker = "#EXT#"

// identityOf decodes the ID token's claims into the identity. A token without tid and
// oid, or whose claims do not decode, is refused (parse_claims_failed).
func identityOf(rawClaims json.RawMessage) (*Identity, error) {
	c, err := oidc.DecodeClaims[claims](rawClaims)
	if err != nil {
		return nil, sessioninfo.NewLoginRefusal(sessioninfo.RefusedClaimsParse, httpio.NewUnauthorizedMessageWithError(err, "Failed to parse ID token claims"))
	}
	if c.Tid == "" || c.Oid == "" {
		return nil, sessioninfo.NewLoginRefusal(sessioninfo.RefusedClaimsParse, httpio.NewUnauthorizedMessage("ID token carries no tid and oid"))
	}

	return &Identity{
		TenantID: c.Tid, ObjectID: c.Oid, Email: c.Email, PreferredUsername: c.PreferredUsername, Name: c.Name,
		Guest: c.guest(), Roles: c.Roles, AMR: c.Amr, SessionID: c.Sid, Claims: rawClaims,
	}, nil
}

// admit checks a verified token's tenant against the slot: the issuer must be the
// tenant's under authority, and the tenant one the slot admits. It returns the
// organization the identity came from as the slot names it.
func admit(authority string, tenants TenantSet, iss, tid string) (string, error) {
	if iss != issuerOf(authority, tid) {
		return "", sessioninfo.NewLoginRefusal(sessioninfo.RefusedIDTokenVerification, httpio.NewUnauthorizedMessage("the ID token's issuer is not its tenant's"))
	}
	organization, ok := tenants.admits(tid)
	if !ok {
		return "", tenants.refusal(tid)
	}

	return organization, nil
}

// guest reports whether the token's account is a guest of the tenant: Entra sets idp
// to the identity provider that authenticated the person when it is not the issuer,
// as it is for a guest, and a guest's user principal name carries the #EXT# marker.
func (c *claims) guest() bool {
	return (c.Idp != "" && c.Idp != c.Iss) || strings.Contains(c.PreferredUsername, guestMarker)
}

// accountRule adapts the application's rule to the contract: the identity the core
// decides is decoded from the claims the sign-in verified. A nil rule stays nil, so the
// standard rule applies.
func accountRule[Tx any](rule AccountRule[Tx]) signin.AccountRule[Tx] {
	if rule == nil {
		return nil
	}

	return func(ctx context.Context, tx Tx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
		identity, err := identityOf(req.Identity.Claims)
		if err != nil {
			return nil, errors.Wrap(err, "azure.identityOf()")
		}
		identity.Organization = req.Identity.Organization

		return rule(ctx, tx, identity)
	}
}

// method is the Azure method over authn. The identity is (tid, oid), its sign-in name
// the preferred username, its organization the tenant as tenants names it. The
// authenticator checks the token's issuer against its tenant and the tenant against
// the slot; both are checked here again, so an authenticator that did not cannot admit
// a tenant outside it. When roles is set, the token's roles claim is reconciled. An
// address is admitted unless it is a personal account's and the slot admits none.
func method(authn Authenticator, roles *signin.RoleReconciler, authority string, tenants TenantSet) *signin.Method {
	m := &signin.Method{
		Name:     sessioninfo.MethodAzure,
		LoginURL: authn.LoginURL,
		Audience: tenants.audience(),
		AdmitsAddress: func(email string) bool {
			return tenants.admitsAddress(signin.AddressDomain(email))
		},
		Begin: func(ctx context.Context, w http.ResponseWriter, r *http.Request, returnURL string) (string, error) {
			chooser, err := signin.SelectAccount(r)
			if err != nil {
				return "", errors.Wrap(err, "signin.SelectAccount()")
			}

			return authn.AuthCodeURL(ctx, w, returnURL, signin.LoginOptions{SelectAccount: chooser})
		},
		Complete: func(ctx context.Context, w http.ResponseWriter, r *http.Request) (*signin.Verified, error) {
			var rawClaims json.RawMessage
			returnURL, _, err := authn.Verify(ctx, w, r, &rawClaims)
			if err != nil {
				return nil, errors.Wrap(err, "azure.Authenticator.Verify()")
			}
			id, err := identityOf(rawClaims)
			if err != nil {
				return nil, err
			}
			iss, err := oidc.Issuer(rawClaims)
			if err != nil {
				return nil, errors.Wrap(err, "oidc.Issuer()")
			}
			organization, err := admit(authority, tenants, iss, id.TenantID)
			if err != nil {
				return nil, err
			}

			v := &signin.Verified{
				Identity: &sessioninfo.Identity{
					Method: sessioninfo.MethodAzure, Connection: id.TenantID, Subject: id.ObjectID, Email: id.Email, Name: id.Name, Claims: rawClaims, IDPAMR: id.AMR,
					SignInName: id.PreferredUsername, Guest: id.Guest, Organization: organization, Attestation: sessioninfo.AttestationOf(id.AMR, tenants.attests(id.TenantID)),
				},
				ReturnURL:         returnURL,
				ProviderSessionID: id.SessionID,
				Issuer:            iss,
			}
			if roles != nil {
				v.RoleNames = signin.RoleNames(id.Roles)
			}

			return v, nil
		},
	}
	if roles != nil {
		m.SyncRoles = func(ctx context.Context, user accesstypes.User, roleNames []string) error {
			return roles.RequireRoles(ctx, user, roleNames)
		}
		m.RecognizesRoles = func(ctx context.Context, roleNames []string) (bool, error) {
			return roles.RecognizesAny(ctx, roleNames)
		}
	}

	return m
}
