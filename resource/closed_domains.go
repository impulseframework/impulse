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

// closedDomainLister is what the permissions SessionPermissions composes answer beside
// UserPermissions: the tenants that seat the person and that this session cannot
// enter. ClosedDomains asks it.
type closedDomainLister interface {
	ClosedDomains(ctx context.Context) ([]accesstypes.Domain, error)
}

// secondFactorOpener is what the permissions SessionPermissions composes answer about
// a tenant that is closed to the session: whether it would open by the administrators'
// way in, had the session a second factor. opensWithSecondFactor asks it.
type secondFactorOpener interface {
	OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error)
}

var (
	_ closedDomainLister = rosterPermissions{}
	_ closedDomainLister = (*maskedPermissions)(nil)
	_ secondFactorOpener = rosterPermissions{}
	_ secondFactorOpener = (*maskedPermissions)(nil)
)

// ClosedDomains lists the tenants that are closed to this session: the domains of the
// application's roster where the person holds at least one grant and that the session
// does not reach (UserPermissions.Reaches), sorted; never nil. A tenant is closed to a
// session when it admits its people through its own directory only and the session
// was not established through that directory. The session's tenant list carries each
// with the reason (UserDomainsHandler), so a person sees the tenant they hold a seat in
// and why this sign-in does not open it; nothing else changes for the tenant, whose
// routes answer the session as a tenant that does not exist. It tells the person
// nothing new: they hold a seat there.
//
// The permissions SessionPermissions composes answer it, for a person's own sign-in.
// A session that views the application as someone else, and one that acts as a role,
// list none: the list is the word a person is given about their own seats and their
// own sign-in, and such a session is neither. Any other UserPermissions, and an
// application that keeps no declarations, list none.
func ClosedDomains(ctx context.Context, perms UserPermissions) ([]accesstypes.Domain, error) {
	lister, ok := perms.(closedDomainLister)
	if !ok {
		return []accesstypes.Domain{}, nil
	}
	closed, err := lister.ClosedDomains(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "resource.ClosedDomains()")
	}

	return closed, nil
}

// ClosedDomains lists the roster's domains where the checker holds a grant and that
// the session does not reach, sorted; never nil. The grant is the checker's own, asked
// before the admission attenuates it: in a tenant the session does not reach, the
// admitted checker holds nothing.
func (p rosterPermissions) ClosedDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	closed := []accesstypes.Domain{}
	if p.roster == nil || p.admission == nil || !p.ownSignIn {
		return closed, nil
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
			closed = append(closed, domain)
		}
	}
	slices.Sort(closed)

	return slices.Compact(closed), nil
}

// ClosedDomains passes through. A mask belongs to an impersonated session, which is
// told of no closed tenant, so the permissions under it answer none.
func (m *maskedPermissions) ClosedDomains(ctx context.Context) ([]accesstypes.Domain, error) {
	return ClosedDomains(ctx, m.UserPermissions)
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
// a session that is not a person's own sign-in, which is told of no closed tenant, and
// in an application that keeps no declarations.
func (p rosterPermissions) OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	if p.admission == nil || !p.ownSignIn {
		return false, nil
	}

	return p.admission.opensWithSecondFactor(ctx, domain)
}

// OpensWithSecondFactor passes through, as ClosedDomains does.
func (m *maskedPermissions) OpensWithSecondFactor(ctx context.Context, domain accesstypes.Domain) (bool, error) {
	return opensWithSecondFactor(ctx, m.UserPermissions, domain)
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
