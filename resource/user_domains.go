package resource

import (
	"net/http"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/tracer"
)

// UserDomain is one tenant of the session's tenant list: its domain, the key in every
// tenant-scoped URL, and its name as the tenant record holds it, for the tenant
// picker to show. Name is the domain itself where the application's roster reads no
// names (WithDomainNames) or the record holds none for the tenant.
type UserDomain struct {
	Domain accesstypes.Domain `json:"domain"`
	Name   string             `json:"name"`
}

// DomainNamer answers a tenant's name, "" for one it holds none for: the tenant roster
// (TenantRoster.Name), which reads the tenant record's name column beside its keys.
type DomainNamer interface {
	Name(domain accesstypes.Domain) string
}

// UserDomainsOption configures UserDomainsHandler.
type UserDomainsOption func(*userDomainsSettings)

type userDomainsSettings struct {
	names DomainNamer
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

// UserDomainsHandler serves the session's tenant list: the domains where the user
// holds at least one grant and that the session reaches, sorted, each with its name
// (UserDomain). It is the tenant picker's question, answered by the library so
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
// not listed. The global scope is never a domain. The answer reports grants,
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

		domains, err := userPermissions(r).Domains(ctx)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(userDomains(domains, settings.names))
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
