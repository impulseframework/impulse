package resource

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// UserDomain is one tenant of the session's tenant list: its domain, the key in every
// tenant-scoped URL, and its name as the tenant record holds it, for the tenant
// picker to show. Name is the domain itself where the application's roster reads no
// names (WithDomainNames) or the record holds none for the tenant.
//
// Closed marks a tenant the person holds a seat in and this session cannot enter
// (ClosedDomains), with the reason in a sentence for the person: what the tenant
// admits and how they signed in ("Acme admits only people who sign in through its own
// directory. You signed in with a password."), a directory said in the words a person
// knows it by (WithDirectoryNames). It is empty, and absent from the JSON,
// for a tenant the session reaches. A picker shows a closed tenant marked, with the
// reason, and never as a place to open. Only a person's own sign-in is told of one: a
// session that views the application as someone else, and one that acts as a role,
// list none closed.
type UserDomain struct {
	Domain accesstypes.Domain `json:"domain"`
	Name   string             `json:"name"`
	Closed string             `json:"closed,omitempty"`
}

// DomainNamer answers a tenant's name, "" for one it holds none for: the tenant roster
// (TenantRoster.Name), which reads the tenant record's name column beside its keys.
type DomainNamer interface {
	Name(domain accesstypes.Domain) string
}

// UserDomainsOption configures UserDomainsHandler.
type UserDomainsOption func(*userDomainsSettings)

type userDomainsSettings struct {
	names       DomainNamer
	directories []DirectoryNamer
}

// WithDomainNames names each tenant of the list from the namer, the application's
// tenant roster: what the generated UserDomains handler passes when the tenant record
// marks its name field (@tenantName). The roster answers from its copy, so naming the
// list reads nothing. A nil namer names nothing.
func WithDomainNames(names DomainNamer) UserDomainsOption {
	return func(s *userDomainsSettings) {
		s.names = names
	}
}

// WithDirectoryNames names the directory a session came through in a closed tenant's
// reason, from the namers of the application's auths: what each auth's sign-in
// registrations call it (DirectoryName). The generated UserDomains handler passes the
// namer of each auth's generated identity client. Without one the reason reads what the
// directory alone says, and it never says a tenant id.
func WithDirectoryNames(namers ...DirectoryNamer) UserDomainsOption {
	return func(s *userDomainsSettings) {
		s.directories = namers
	}
}

// UserDomainsHandler serves the session's tenant list: the domains where the user
// holds at least one grant, sorted, each with its name (UserDomain), and each that
// the session does not reach marked closed, with the reason. It is the tenant
// picker's question, answered by the library so
// applications wire nothing — the generated router registers it on the default
// outlet beside the digest. The picker shows a tenant's name: the people of a tenant
// cannot read the tenant record, a global resource, so the list carries the name for
// them (WithDomainNames).
//
// The predicate is concealed tenancy's own foothold test over the session's
// admission (UserPermissions.HasGrants, UserPermissions.Reaches), so a domain
// listed here is exactly a domain whose routes answer the user with ordinary
// 403s rather than a concealing 404; the picker and the guard can never
// disagree. A tenant is offered only when the session may enter it, and the
// application's tenant declaration decides (TenantDeclarations): a tenant
// whose declaration does not admit the way the session was established is
// listed closed (UserDomain.Closed), since the person holds a seat there and is
// owed the reason, and its routes answer the session with the concealing 404 all
// the same. The global scope is never a domain. The answer reports grants,
// not tenants: a domain the application has since removed still lists while
// grants in it remain, and existence is the tenant roster's
// (TenantRoster.Has). Structural and non-folding, so it caches cleanly per
// user and session. The payload is always a JSON array — an empty membership
// is [], never null.
func UserDomainsHandler(userPermissions func(r *http.Request) UserPermissions, opts ...UserDomainsOption) http.HandlerFunc {
	var settings userDomainsSettings
	for _, opt := range opts {
		opt(&settings)
	}

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		perms := userPermissions(r)
		domains, err := perms.Domains(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		closed, err := ClosedDomains(ctx, perms)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		out := userDomains(domains, settings.names)
		if len(closed) > 0 {
			// Only a session's permissions list a closed tenant, so the session is there
			// to say how it was established.
			session := sessioninfo.DataFromCtx(ctx)
			for _, tenant := range userDomains(closed, settings.names) {
				tenant.Closed = closedReason(tenant.Name, session, settings.directories)
				out = append(out, tenant)
			}
			slices.SortFunc(out, func(a, b UserDomain) int {
				return cmp.Compare(a.Domain, b.Domain)
			})
		}

		return httpio.NewEncoder(w).Ok(out)
	})
}

// userDomains names each domain, the domain itself where the namer holds no name;
// never nil.
func userDomains(domains []accesstypes.Domain, names DomainNamer) []UserDomain {
	out := make([]UserDomain, 0, len(domains))
	for _, domain := range domains {
		name := ""
		if names != nil {
			name = names.Name(domain)
		}
		if name == "" {
			name = string(domain)
		}
		out = append(out, UserDomain{Domain: domain, Name: name})
	}

	return out
}
