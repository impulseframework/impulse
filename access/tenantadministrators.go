package access

import (
	"context"
	"slices"
	"time"

	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/tracer"
)

// A tenant's administrators. An application names the roles whose holders
// administer a tenant (WithTenantAdministratorRoles), and a person administers
// a tenant when they hold one of them in the tenant itself, inside its period.
// It is the question a tenant's sign-in settings ask before they keep a person
// out: a tenant that admits its people through its directory only still admits
// its own administrators with a password and a second factor, so a tenant whose
// directory is broken is not a tenant nobody can mend (the session library's
// signin.TenantDeclaration.AdmitsAdministrator is the decision). The sign-in
// asks it of the store (UserManager.TenantsAdministered) and every request asks
// it of the snapshot (Client.UserAdministersTenant); both read a role through
// AdministersTenant, the one statement of where a role must be held to count.
// The generated user management reads the same roles (TenantAdministratorRoles):
// while a tenant is directory only, who holds one of them in it, and the account
// of a person who does, is changed only by a caller whose own session the tenant
// admits, so nobody the tenant keeps out makes themselves its administrator.

// AdministersTenant reports the tenant a role makes its holder an
// administrator of, held where scope says: by a membership of the person's own
// (kept is nil) or by a group of people kept where kept says. A role counts
// when it is one of administrators and is held in the tenant itself:
//
//   - a membership of the person's own in the tenant's domain;
//   - a role a group kept in that tenant holds there. A tenant's own group is
//     written in the tenant, its people and its roles alike, so it gives nobody
//     what a membership in the tenant would not, and a tenant that keeps its
//     administrators in a group has administrators.
//
// A role held in every tenant does not count, and neither does one a group
// kept for the whole application holds in the tenant: both are how the
// application's own people are seated everywhere, and a tenant's sign-in
// settings shut those people out with everyone else.
//
// It is exported for a caller that judges a change to who holds a role before
// the engine reads it, the generated user management while a tenant is
// directory only, so that what it guards and what the engine counts are one
// statement.
func AdministersTenant(administrators []accesstypes.Role, scope accesstypes.PolicyScope, role accesstypes.Role, kept *accesstypes.PolicyScope) (accesstypes.Domain, bool) {
	domain, inDomain := scope.Domain()
	if !inDomain || domain == "" || scope.Axis() != "" {
		return "", false
	}
	if kept != nil && *kept != scope {
		return "", false
	}
	if !slices.Contains(administrators, role) {
		return "", false
	}

	return domain, true
}

// tenantAdministrators are the people who administer each tenant, with the
// periods their holdings count in: what a snapshot answers
// userAdministersTenant from. A person is listed once for each holding that
// makes them an administrator, so a role held twice, or through two groups,
// counts while either does.
type tenantAdministrators map[accesstypes.Domain]map[accesstypes.User][]accesstypes.Period

// compileTenantAdministrators reads who administers each tenant out of the
// policy records: every membership and every group's role administersTenant
// counts, with the period it counts in, which for a group's role is the one in
// which the person's place in the group and the group's role both count. A
// role's own row is not asked for: a membership in a role the release does not
// define gives nothing in the tenant, and its holder has no foothold there
// whatever this says. It is nil where the application named no administrator
// role.
func compileTenantAdministrators(records *policy.Records, administrators []accesstypes.Role) tenantAdministrators {
	if len(administrators) == 0 || records == nil {
		return nil
	}
	kept := make(map[accesstypes.Group]accesstypes.PolicyScope, len(records.Groups))
	for _, group := range records.Groups {
		kept[group.ID] = group.Scope
	}
	members := make(map[accesstypes.Group][]policy.GroupMember)
	for _, member := range records.GroupMembers {
		members[member.Group] = append(members[member.Group], member)
	}

	compiled := make(tenantAdministrators)
	add := func(domain accesstypes.Domain, user accesstypes.User, period accesstypes.Period) {
		if compiled[domain] == nil {
			compiled[domain] = make(map[accesstypes.User][]accesstypes.Period)
		}
		compiled[domain][user] = append(compiled[domain][user], period)
	}
	for i := range records.Memberships {
		m := &records.Memberships[i]
		switch m.Member.Kind {
		case policy.SubjectUser:
			if domain, ok := AdministersTenant(administrators, m.Scope, m.Role, nil); ok {
				add(domain, accesstypes.User(m.Member.Name), m.Period)
			}
		case policy.SubjectGroup:
			group := accesstypes.Group(m.Member.Name)
			where, exists := kept[group]
			if !exists {
				continue
			}
			domain, ok := AdministersTenant(administrators, m.Scope, m.Role, &where)
			if !ok {
				continue
			}
			for _, member := range members[group] {
				add(domain, member.User, m.Period.Intersect(member.Period))
			}
		default:
			// A role that inherits from an administrator's role makes nobody an
			// administrator: the application names the roles themselves.
		}
	}

	return compiled
}

// administers reports whether user administers the tenant at the instant: a
// holding of theirs counts then.
func (t tenantAdministrators) administers(user accesstypes.User, domain accesstypes.Domain, at time.Time) bool {
	return slices.ContainsFunc(t[domain][user], func(period accesstypes.Period) bool { return period.Contains(at) })
}

// userAdministersTenant reports whether user administers the tenant at the
// instant, as the records the snapshot was compiled from say.
func (s *snapshot) userAdministersTenant(user accesstypes.User, domain accesstypes.Domain, at time.Time) bool {
	return s.administrators.administers(user, domain, at)
}

// userAdministersTenant answers from the snapshot the checks are answered
// from, at the engine's own clock, so a role taken away, or one whose period
// has ended, administers nothing at the next request.
func (s *snapshotEngine) userAdministersTenant(_ context.Context, user accesstypes.User, domain accesstypes.Domain) (bool, error) {
	at := s.clock()
	snap, err := s.snapshotAt(at)
	if err != nil {
		return false, err
	}

	return snap.userAdministersTenant(user, domain, at), nil
}

// UserAdministersTenant reports whether user administers the tenant: they hold
// one of the roles the application names as a tenant's administrator's
// (WithTenantAdministratorRoles) in the tenant itself, inside its period, by a
// membership of their own in the tenant's domain or through a group kept in
// that tenant. A role held in every tenant, or through a group kept for the
// whole application, administers no tenant in this sense. It is answered from
// the in-memory policy snapshot like the checks (safe inside transactions),
// and is the question a session's reach into a tenant asks of the
// session's actor where the tenant admits its people through its directory
// only: the tenant's own administrators keep a way in. A Client built without
// the option answers false for everyone.
func (c *Client) UserAdministersTenant(ctx context.Context, user accesstypes.User, domain accesstypes.Domain) (bool, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return c.evaluator.userAdministersTenant(ctx, user, domain)
}

// TenantAdministratorRoles returns the roles the client was told administer a
// tenant (WithTenantAdministratorRoles), none without the option. An auth's
// generated identity client reads them here and hands them to the tenant's list
// of its people, so the sign-in, the session's reach and the list are answered
// over one set of names.
func (c *Client) TenantAdministratorRoles() []accesstypes.Role {
	return slices.Clone(c.tenantAdministrators)
}

// TenantsAdministered lists the tenants user administers now, sorted: the
// tenant domains where they hold one of the client's tenant administrator
// roles in the tenant itself (administersTenant), by a membership of their own
// or through a group kept in the tenant, less the holdings outside their
// period. It reads the store, as UserRoles does, and is what a sign-in decides
// the administrators' way into a directory-only tenant by.
func (u *userManager) TenantsAdministered(ctx context.Context, user accesstypes.User) ([]accesstypes.Domain, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(u.tenantAdministrators) == 0 {
		return nil, nil
	}
	var tenants []accesstypes.Domain
	add := func(domain accesstypes.Domain) {
		if !slices.Contains(tenants, domain) {
			tenants = append(tenants, domain)
		}
	}
	memberships, err := u.store.userMemberships(ctx, user)
	if err != nil {
		return nil, err
	}
	for i := range memberships {
		if domain, ok := AdministersTenant(u.tenantAdministrators, memberships[i].Scope, memberships[i].Role, nil); ok {
			add(domain)
		}
	}
	roles, err := u.store.userGroupRoles(ctx, user)
	if err != nil {
		return nil, err
	}
	for i := range roles {
		if domain, ok := AdministersTenant(u.tenantAdministrators, roles[i].Scope, roles[i].Role, &roles[i].Group.Scope); ok {
			add(domain)
		}
	}
	slices.Sort(tenants)

	return tenants, nil
}
