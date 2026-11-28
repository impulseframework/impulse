package resource

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// unavailableDomainLister is what the permissions SessionPermissions composes answer
// beside UserPermissions: the tenants that seat the person and that this session cannot
// enter. UnavailableDomains asks it.
type unavailableDomainLister interface {
	UnavailableDomains(ctx context.Context) ([]accesstypes.Domain, error)
}

// secondFactorOpener is what the permissions SessionPermissions composes answer about
// a tenant that is closed to the session: whether it would open by the administrators'
// way in, had the session a second factor. opensWithSecondFactor asks it.
type secondFactorOpener interface {
	OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error)
}

// disabledTenantTeller is what the permissions SessionPermissions composes answer about
// a tenant the session cannot enter: whether it is disabled. tenantDisabled asks it.
type disabledTenantTeller interface {
	TenantDisabled(ctx context.Context, domain accesstypes.Domain) (bool, error)
}

// unenteredDomainLister is what the permissions SessionPermissions composes answer
// beside UserPermissions: the tenants of the application's roster whose sign-in settings
// do not admit this session. unenteredDomains asks it.
type unenteredDomainLister interface {
	UnenteredDomains(ctx context.Context) ([]accesstypes.Domain, error)
}

var (
	_ unenteredDomainLister = rosterPermissions{}
	_ unenteredDomainLister = (*maskedPermissions)(nil)
)

var (
	_ unavailableDomainLister = rosterPermissions{}
	_ unavailableDomainLister = (*maskedPermissions)(nil)
	_ secondFactorOpener      = rosterPermissions{}
	_ secondFactorOpener      = (*maskedPermissions)(nil)
	_ disabledTenantTeller    = rosterPermissions{}
	_ disabledTenantTeller    = (*maskedPermissions)(nil)
)

// UnavailableDomains lists the tenants this session cannot enter though the person
// holds a seat in them: the domains of the application's roster where the person holds
// at least one grant and that the session does not reach (UserPermissions.Reaches),
// sorted; never nil. A tenant is one of them when it admits its people through its own
// directory only and the session was not established through that directory, or when
// it is disabled. The session's tenant list carries each with the reason
// (UserDomainsHandler), so a person sees the tenant they hold a seat in and why this
// sign-in does not open it; nothing else changes for such a tenant, whose routes answer
// the session as a tenant that does not exist. It tells the person nothing new: they
// hold a seat there.
//
// The permissions SessionPermissions composes answer it, for a person's own sign-in.
// A session that views the application as someone else, and one that acts as a role,
// list none: the list is the word a person is given about their own seats and their
// own sign-in, and such a session is neither. Any other UserPermissions, and an
// application that keeps no declarations, list none.
func UnavailableDomains(ctx context.Context, perms UserPermissions) ([]accesstypes.Domain, error) {
	lister, ok := perms.(unavailableDomainLister)
	if !ok {
		return []accesstypes.Domain{}, nil
	}
	unavailable, err := lister.UnavailableDomains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.UnavailableDomains()")
	}

	return unavailable, nil
}

// UnavailableDomains lists the roster's domains where the checker holds a grant and that
// the session does not reach, sorted; never nil. The grant is the checker's own, asked
// before the admission attenuates it: in a tenant the session does not reach, the
// admitted checker holds nothing.
func (p rosterPermissions) UnavailableDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	unavailable := []accesstypes.Domain{}
	if p.roster == nil || p.admission == nil || !p.ownSignIn {
		return unavailable, nil
	}
	all, err := p.roster(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.DomainRoster")
	}
	for _, domain := range all {
		has, err := p.admission.UserPermissionChecker.HasGrants(ctx, accesstypes.DomainScope(domain))
		if err != nil {
			return nil, errors.Wrap(err, "resource.UserPermissionChecker.HasGrants()")
		}
		if !has {
			continue
		}
		reaches, err := p.admission.Reaches(ctx, domain)
		if err != nil {
			return nil, err
		}
		if !reaches {
			unavailable = append(unavailable, domain)
		}
	}
	slices.Sort(unavailable)

	return slices.Compact(unavailable), nil
}

// UnavailableDomains passes through. A mask belongs to an impersonated session, which is
// told of no such tenant, so the permissions under it answer none.
func (m *maskedPermissions) UnavailableDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	return UnavailableDomains(ctx, m.UserPermissions)
}

// unenteredDomains lists the tenants of the application's roster whose sign-in settings
// do not admit this session (AdmitsSession), sorted; never nil. It is the capability
// plan's question for a method a row offers only to a caller whose session enters the
// row's tenants (TenantsToEnter): the same question the method's own refusal asks of
// each tenant, asked once for the roster. Whether a tenant is disabled is not asked: a
// disabled tenant is listed where its settings would keep the session out, as before it
// was disabled. Unlike UnavailableDomains it is asked of every session, a person's own
// sign-in or not, and of tenants the session holds no grant in. The permissions
// SessionPermissions composes answer it; any other UserPermissions, and an application
// that keeps no declarations or no tenants, list none, and the row then offers what the
// grants allow: the answer shapes what a page offers, and the method refuses what it
// refuses either way.
func unenteredDomains(ctx context.Context, perms UserPermissions) ([]accesstypes.Domain, error) {
	lister, ok := perms.(unenteredDomainLister)
	if !ok {
		return []accesstypes.Domain{}, nil
	}
	unentered, err := lister.UnenteredDomains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.unenteredDomains()")
	}

	return unentered, nil
}

// UnenteredDomains lists the roster's domains whose sign-in settings do not admit the
// session (the admission's admitsSession), sorted; never nil. An application that keeps
// no declarations or no tenants lists none: every tenant's settings admit every session.
func (p rosterPermissions) UnenteredDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	unentered := []accesstypes.Domain{}
	if p.roster == nil || p.admission == nil {
		return unentered, nil
	}
	all, err := p.roster(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.DomainRoster")
	}
	for _, domain := range all {
		admits, err := p.admission.admitsSession(ctx, domain)
		if err != nil {
			return nil, err
		}
		if !admits {
			unentered = append(unentered, domain)
		}
	}
	slices.Sort(unentered)

	return slices.Compact(unentered), nil
}

// UnenteredDomains passes through: a mask attenuates what the session may do, never
// what a tenant's sign-in settings say of it (AdmitsSession).
func (m *maskedPermissions) UnenteredDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	return unenteredDomains(ctx, m.UserPermissions)
}

// opensWithSecondFactor reports whether domain, a tenant closed to the session, would
// open to it by the administrators' way in had the session a second factor: the
// session's own person administers the tenant and signed in with a password, and only
// the code is missing. The permissions SessionPermissions composes answer it, for a
// person's own sign-in; any other UserPermissions answer no.
func opensWithSecondFactor(ctx context.Context, perms UserPermissions, domain accesstypes.Domain) (bool, error) {
	opener, ok := perms.(secondFactorOpener)
	if !ok {
		return false, nil
	}
	opens, err := opener.OpensWithSecondFactor(ctx, domain)
	if err != nil {
		return false, errors.Wrap(err, "resource.opensWithSecondFactor()")
	}

	return opens, nil
}

// OpensWithSecondFactor reports whether domain would open to the session by the
// administrators' way in had the session a second factor (admittedPermissions): no for
// a session that is not a person's own sign-in, which is told of no tenant it cannot
// enter, and in an application that keeps no declarations.
func (p rosterPermissions) OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	if p.admission == nil || !p.ownSignIn {
		return false, nil
	}

	return p.admission.opensWithSecondFactor(ctx, domain)
}

// OpensWithSecondFactor passes through, as UnavailableDomains does.
func (m *maskedPermissions) OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	return opensWithSecondFactor(ctx, m.UserPermissions, domain)
}

// tenantDisabled reports whether domain, a tenant the session cannot enter, is
// disabled. The permissions SessionPermissions composes answer it, for a person's own
// sign-in; any other UserPermissions answer no.
func tenantDisabled(ctx context.Context, perms UserPermissions, domain accesstypes.Domain) (bool, error) {
	teller, ok := perms.(disabledTenantTeller)
	if !ok {
		return false, nil
	}
	disabled, err := teller.TenantDisabled(ctx, domain)
	if err != nil {
		return false, errors.Wrap(err, "resource.tenantDisabled()")
	}

	return disabled, nil
}

// TenantDisabled reports whether domain is disabled (admittedPermissions): no for a
// session that is not a person's own sign-in, which is told of no tenant it cannot
// enter, and in an application that keeps no declarations.
func (p rosterPermissions) TenantDisabled(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	if p.admission == nil || !p.ownSignIn {
		return false, nil
	}

	return p.admission.tenantDisabled(ctx, domain)
}

// TenantDisabled passes through, as UnavailableDomains does.
func (m *maskedPermissions) TenantDisabled(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	return tenantDisabled(ctx, m.UserPermissions, domain)
}

// disabledReason is the sentence the session's tenant list gives for a tenant that is
// disabled, whatever way the person signed in and whether or not they administer it.
// name is the tenant's name as the list shows it.
func disabledReason(name string) string {
	return fmt.Sprintf("%s is disabled.", name)
}

// administratorsWaySentence is the last sentence of a tenant's closed reason for the
// tenant's own administrator whose password session carries no second factor: the way
// in they hold, and what to do to take it. A sign-in of theirs with the password is
// held for the code (signin.TenantPolicy), and the session it makes enters the tenant.
const administratorsWaySentence = "As one of its administrators you can still enter it: sign out, then sign in again with your password and the code we email you."

// closedReason is the sentence the session's tenant list gives for a tenant that is
// closed to the session: what the tenant admits, and how this session was established.
// name is the tenant's name as the list shows it, and namers say what the application's
// sign-in registrations call the directory the session came through. administrator
// says the tenant would open to the person by the administrators' way in had the
// session a second factor (opensWithSecondFactor), and the reason then ends with what
// to do.
func closedReason(name string, session *sessioninfo.SessionData, namers []DirectoryNamer, administrator bool) string {
	reason := fmt.Sprintf("%s admits only people who sign in through its own directory. %s", name, establishedSentence(session, namers))
	if administrator {
		reason += " " + administratorsWaySentence
	}

	return reason
}

// establishedSentence says how a session was established, to the person whose session
// it is: the second sentence of a tenant's closed reason. A directory is said in the
// words a person knows it by (DirectoryName), its kind and the name a registration
// gives it, or the label of its button, and never by its connection.
func establishedSentence(session *sessioninfo.SessionData, namers []DirectoryNamer) string {
	established, ok := session.EstablishedBy()
	directory := signin.Directory{Method: established.Method, Connection: established.Connection}
	switch {
	case !ok:
		return "This session was not signed in through it."
	case established.Method == sessioninfo.MethodPassword:
		return "You signed in with a password."
	case directory.PersonalAccounts():
		return fmt.Sprintf("You signed in with %s.", personalAccountName(directory))
	case directory.Organization():
		return fmt.Sprintf("You signed in through %s.", DirectoryName(directory, namers...))
	case established.Method == sessioninfo.MethodImpersonation:
		return "This session is viewing the application as someone else, which is no sign-in through its directory."
	default:
		return "This session was not signed in through it."
	}
}

// personalAccountName is what a sentence calls a personal account in its middle.
func personalAccountName(directory signin.Directory) string {
	if directory.Method == sessioninfo.MethodAzure {
		return "a personal Microsoft account"
	}

	return "a personal Google account"
}
