package session

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// The first administrator of a deployed application comes in as everyone after them
// does: by an invitation mailed to their own address, which they accept by signing in
// through a method of the Auth. The deployment names the address, and its release asks
// for the invitation (InviteFirstAdministrator) while nobody of the Auth holds the Super
// Administrator role. No account is created ahead of the person and no password is
// generated for them, so nothing stands that somebody other than the mailbox's owner
// could sign in with.
//
// The first administrator is the deployment's first super administrator, the one person
// who can make another, so the invitation gives the Super Administrator role
// (accesstypes.SuperAdministrator) beside what else the application names.
//
// The role is also what the release asks about. The invitation exists so that somebody
// administers the Auth, and an account that holds nothing (a person who signed up, one
// a directory seated, a persona a test environment seeds) administers nothing and stops
// no invitation. A holder whose account is disabled is a holder still: the last holder
// who can sign in keeps the role and the account whoever asks, so the question here is
// who holds the role, not who can sign in.
//
// And the role is how the deployment's own invitation is told from every other: it is
// an invitation into the application that gives the Super Administrator role and that no
// user of the Auth sent. Only the application acting for itself writes one; a user who
// invites to that role is a super administrator of the Auth and is named on the row.
// Those are the invitations this file reads and withdraws, and it touches no other: one
// a user sent, one into a tenant, one that makes a tenant, and one that gives other
// roles alone (an administrator of another Auth may send such a one, and its row names
// no user of this Auth) are never the first administrator's.
//
// Nothing here locks: a deployment runs one release at a time, and the step is its.

// The refusals of the first administrator's invitation. Each says what is wrong and
// what to do; the caller says which Auth and which setting it read.
var (
	// ErrFirstAdministratorUnnamed refuses an invitation with no address to mail it to.
	ErrFirstAdministratorUnnamed = errors.New("no first administrator is named: nobody holds the Super Administrator role yet, and the first administrator " +
		"comes in by an invitation mailed to the address the deployment names. Name the person's email address for this environment, then run this step again")
	// ErrFirstAdministratorNoMailbox refuses an invitation the Auth could only write to
	// a log: whoever opens an invitation's link accepts it, so the link reaches the
	// mailbox it was sent to and nothing else.
	ErrFirstAdministratorNoMailbox = errors.New("the application has no mailbox to send as, so the first administrator's invitation was not sent: " +
		"mail is written to the log here, and an invitation's link is never written to a log, since whoever opens it becomes the administrator. " +
		"Give this environment its mailbox, then run this step again")
	// ErrFirstAdministratorRoleHeld refuses to send the invitation again once somebody
	// holds the Super Administrator role.
	ErrFirstAdministratorRoleHeld = errors.New("somebody holds the Super Administrator role, so no first administrator is invited: " +
		"the invitation is sent again only until somebody holds that role. A person who holds it invites the others from the application")
)

// RoleHolders is the role store the first administrator's invitation asks who holds the
// Super Administrator role: it lists the people who hold a role in a place, each as the
// permission store names them, by the id of their account. The access library's user
// manager is one (access.UserManager.RoleUsers), and an auth package hands the manager
// of its own permission engine, the one its acceptance hook gives the roles through.
type RoleHolders interface {
	// RoleUsers returns the users who hold role in scope now.
	RoleUsers(ctx context.Context, scope accesstypes.PolicyScope, role accesstypes.Role) ([]accesstypes.User, error)
}

// FirstAdministratorRequest names the person a deployment invites as the Auth's first
// administrator, and what the invitation gives them.
type FirstAdministratorRequest struct {
	// Email is the address the deployment names for the person, where the invitation
	// is mailed.
	Email string
	// RoleNames are the roles the acceptance gives, as the application's acceptance
	// hook understands them (SetInvitationAccepted): the Super Administrator role
	// (accesstypes.SuperAdministrator) and what else the application names.
	RoleNames []string
}

// FirstAdministratorOutcome says what a call did about the first administrator's
// invitation.
type FirstAdministratorOutcome int

const (
	// FirstAdministratorInvited says an invitation was mailed to the address.
	FirstAdministratorInvited FirstAdministratorOutcome = iota + 1
	// FirstAdministratorStanding says an invitation to the address, with the same
	// roles, stands: nothing was mailed.
	FirstAdministratorStanding
	// FirstAdministratorRoleHeld says somebody holds the Super Administrator role:
	// nobody is invited.
	FirstAdministratorRoleHeld
)

// FirstAdministrator is what a call did about the first administrator's invitation. It
// never carries the invitation's link or its token: those are in the message alone.
type FirstAdministrator struct {
	// Outcome says what was done.
	Outcome FirstAdministratorOutcome
	// Email is the address the invitation that stands after the call was mailed to,
	// bare, and ExpiresAt when it stops being accepted; both zero when somebody holds
	// the Super Administrator role.
	Email     string
	ExpiresAt time.Time
	// Withdrawn are the addresses of the invitations the call withdrew, whose links no
	// longer open: one the deployment no longer names, one whose roles are no longer
	// the ones asked for, and the standing one a fresh invitation replaces.
	Withdrawn []string
}

// InviteFirstAdministrator is the step a release runs for the Auth's first
// administrator. While nobody of the Auth holds the Super Administrator role it leaves
// exactly one invitation standing: the one mailed to req.Email with req.RoleNames.
// roles is the role store that says who holds the role (RoleHolders), the user manager
// of the Auth's own permission engine; a holder counts when the id is an account of
// this Auth, disabled or not, and an account that holds no such role stops nothing.
//
//   - Somebody holds the Super Administrator role: nobody is invited
//     (FirstAdministratorRoleHeld), nothing is mailed and no mailbox is asked for.
//     Every person after the first is invited from the application. An invitation of
//     the deployment's that still stands for an address it no longer names is
//     withdrawn all the same, so no link stays open for an address the deployment took
//     back, and one to the address still named is left to be accepted.
//   - An invitation to the address with those roles stands: nothing is mailed
//     (FirstAdministratorStanding), so a release while the person has not yet accepted
//     sends no second message.
//   - None stands, because none was sent or the one sent has run out
//     (MailSettings.InvitationTimeout, seven days unless set): one is mailed
//     (FirstAdministratorInvited).
//   - One stands that the deployment no longer names (another address, or other roles):
//     it is withdrawn, its link no longer opens, and the address named now is mailed.
//
// While nobody holds the role it refuses, with nothing mailed, an empty address
// (ErrFirstAdministratorUnnamed) and an Auth whose sender delivers nothing
// (ErrFirstAdministratorNoMailbox, mail.Delivers). Whoever holds the role it refuses a
// call the application built wrongly, before anything is read: roles that do not give
// the Super Administrator role, no role store, and an Auth with no acceptance hook
// (SetInvitationAccepted), whose accepted invitation would give nobody a role.
// An invitation the deployment no longer names is withdrawn before either refusal, so a
// refused run never leaves a link standing for an address the deployment took back, and
// the error says which were withdrawn. The checks every invitation is held to apply to
// the address too (Invite): one no sign-in method of the Auth could admit is refused,
// and a message that could not be sent leaves no invitation standing.
func (p *AuthAPI[S, U, Tx]) InviteFirstAdministrator(ctx context.Context, roles RoleHolders, req *FirstAdministratorRequest) (*FirstAdministrator, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return p.inviteFirstAdministrator(ctx, roles, req, false)
}

// ReinviteFirstAdministrator withdraws the first administrator's standing invitation
// and mails a fresh one to req.Email, for a message that was lost: the earlier link no
// longer opens. It is InviteFirstAdministrator in every other respect, except that it
// is refused once somebody holds the Super Administrator role
// (ErrFirstAdministratorRoleHeld): there is somebody to invite the others, and nothing
// is withdrawn. The standing invitation is left as it is when the fresh one cannot be
// mailed for want of a mailbox, so the refusal takes nothing away.
func (p *AuthAPI[S, U, Tx]) ReinviteFirstAdministrator(ctx context.Context, roles RoleHolders, req *FirstAdministratorRequest) (*FirstAdministrator, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	result, err := p.inviteFirstAdministrator(ctx, roles, req, true)
	if err != nil {
		return nil, err
	}
	if result.Outcome == FirstAdministratorRoleHeld {
		return nil, ErrFirstAdministratorRoleHeld
	}

	return result, nil
}

// inviteFirstAdministrator is InviteFirstAdministrator, and with fresh
// ReinviteFirstAdministrator, which replaces the standing invitation too.
func (p *AuthAPI[S, U, Tx]) inviteFirstAdministrator(ctx context.Context, holders RoleHolders, req *FirstAdministratorRequest, fresh bool) (*FirstAdministrator, error) {
	roles, err := p.firstAdministratorAsked(holders, req)
	if err != nil {
		return nil, err
	}
	held, err := p.superAdministratorHeld(ctx, holders)
	if err != nil {
		return nil, err
	}

	// The address as an invitation keeps it, bare, so one written with a display name is
	// the same address at the next release. One that is no address names nobody: nothing
	// standing is the deployment's any more, and the refusal follows the withdrawals.
	email, bare, notAddress := strings.TrimSpace(req.Email), "", error(nil)
	if email != "" {
		bare, notAddress = address(email)
	}
	if held {
		if fresh {
			return &FirstAdministrator{Outcome: FirstAdministratorRoleHeld}, nil
		}

		return p.withdrawTakenBack(ctx, bare, roles)
	}
	standing, stale, err := p.firstAdministratorInvitations(ctx, bare, roles)
	if err != nil {
		return nil, err
	}
	result := &FirstAdministrator{}
	for _, inv := range stale {
		accepted, err := p.withdrawFirstAdministrator(ctx, holders, inv, result)
		if err != nil {
			return nil, err
		}
		if accepted {
			return &FirstAdministrator{Outcome: FirstAdministratorRoleHeld, Withdrawn: result.Withdrawn}, nil
		}
	}
	switch {
	case email == "":
		return nil, withdrawnBefore(ErrFirstAdministratorUnnamed, result.Withdrawn)
	case notAddress != nil:
		return nil, withdrawnBefore(notAddress, result.Withdrawn)
	case standing != nil && !fresh:
		result.Outcome, result.Email, result.ExpiresAt = FirstAdministratorStanding, standing.Email, standing.ExpiresAt

		return result, nil
	}

	m := p.auth.settings.mail
	if !mail.Delivers(m.sender) {
		return nil, withdrawnBefore(ErrFirstAdministratorNoMailbox, result.Withdrawn)
	}
	if standing != nil {
		accepted, err := p.withdrawFirstAdministrator(ctx, holders, standing, result)
		if err != nil {
			return nil, err
		}
		if accepted {
			return &FirstAdministrator{Outcome: FirstAdministratorRoleHeld, Withdrawn: result.Withdrawn}, nil
		}
	}
	invitation, err := p.Invite(ctx, &InvitationRequest{Email: bare, RoleNames: roles})
	if err != nil {
		return nil, withdrawnBefore(err, result.Withdrawn)
	}
	result.Outcome, result.Email, result.ExpiresAt = FirstAdministratorInvited, invitation.Email, invitation.ExpiresAt

	return result, nil
}

// firstAdministratorAsked holds a call to what the step needs before anything is read,
// and answers the roles the invitation gives, each once. Each refusal is a mistake in
// how the application is built, which no environment changes, so it is made whoever
// holds the role: the roles do not give the Super Administrator role; no role store was
// handed to ask who holds it; or the Auth has no acceptance hook, where an accepted
// invitation would give its person nothing, nobody would come to hold the role, and
// every release after would mail the address again.
func (p *AuthAPI[S, U, Tx]) firstAdministratorAsked(holders RoleHolders, req *FirstAdministratorRequest) ([]string, error) {
	roles := distinctRoleNames(req.RoleNames)
	switch {
	case !slices.Contains(roles, superAdministratorRole):
		return nil, errors.Newf("the first administrator's invitation does not give the %s role: the first administrator is the deployment's first super administrator, "+
			"the one person who can make another, so the roles the invitation names begin with that role", superAdministratorRole)
	case holders == nil:
		return nil, errors.New("the first administrator's invitation was handed no role store to ask who holds the Super Administrator role: " +
			"pass the user manager of the Auth's own permission engine (access.Client.UserManager), the one its acceptance hook gives the roles through")
	case p.auth.acceptInvitation == nil:
		return nil, errors.New("the Auth has no acceptance hook, so an accepted invitation would give its person no role and nobody would come to hold the Super Administrator role: " +
			"set the hook that gives an invitation its roles when the Auth is built (SetInvitationAccepted, with the generated identity client's InvitationAccepted)")
	}

	return roles, nil
}

// withdrawTakenBack is what a release does once somebody holds the Super Administrator
// role. Nobody is invited, and one thing is still done: an invitation of the
// deployment's that stands for an address the deployment no longer names is withdrawn,
// whatever roles it gives beside the Super Administrator role, so a link mailed to an
// address the deployment took back does not stay open until it runs out. That is the
// case where somebody came to hold the role before the invited person accepted, as
// where a super administrator was made another way while the invitation stood. An
// invitation to the address still named is left to be accepted, with the roles it was
// sent with.
func (p *AuthAPI[S, U, Tx]) withdrawTakenBack(ctx context.Context, email string, roles []string) (*FirstAdministrator, error) {
	result := &FirstAdministrator{Outcome: FirstAdministratorRoleHeld}
	_, stale, err := p.firstAdministratorInvitations(ctx, email, roles)
	if err != nil {
		return nil, err
	}
	for _, inv := range stale {
		if email != "" && sameAddress(inv.Email, email) {
			continue
		}
		// One that no longer stands, accepted or run out since it was read, needs no
		// withdrawal.
		if _, err := p.revokeFirstAdministrator(ctx, inv, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// superAdministratorHeld reports whether somebody of the Auth holds the Super
// Administrator role: the role store lists a holder of it in the global partition, the
// one place the role is held, whose id is an account of this Auth. The account may be
// disabled, and may never have signed in. A holder the store names who is no account
// here (a person of another Auth over the same store, a row left behind) is nobody of
// this Auth and is passed over.
func (p *AuthAPI[S, U, Tx]) superAdministratorHeld(ctx context.Context, holders RoleHolders) (bool, error) {
	users, err := holders.RoleUsers(ctx, accesstypes.GlobalPolicyScope(), accesstypes.SuperAdministrator)
	if err != nil {
		return false, errors.Wrap(err, "session.RoleHolders.RoleUsers()")
	}
	for _, user := range users {
		id, err := impulse.UUIDFromString(string(user))
		if err != nil {
			continue
		}
		_, err = p.auth.storage.User(ctx, id)
		switch {
		case err == nil:
			return true, nil
		case !httpio.HasNotFound(err):
			return false, errors.Wrap(err, "sessionstorage.AuthStore.User()")
		}
	}

	return false, nil
}

// superAdministratorRole is the Super Administrator role as an invitation names it.
const superAdministratorRole = string(accesstypes.SuperAdministrator)

// firstAdministratorInvitations reads the deployment's own standing invitations (an
// invitation into the application that gives the Super Administrator role and that no
// user of the Auth sent) and tells them apart: standing is
// the one mailed to email, a bare address, with exactly roles, nil for none, and stale
// every other, which the deployment no longer names; with no address ("") every one is
// stale. Of two that match, the one that stands longest is kept and the other is stale,
// so one link stands.
func (p *AuthAPI[S, U, Tx]) firstAdministratorInvitations(ctx context.Context, email string, roles []string) (standing *sessioninfo.Invitation, stale []*sessioninfo.Invitation, err error) {
	links, err := p.auth.storage.LiveMailLinks(ctx, sessionstorage.MailLinkInvitation)
	if err != nil {
		return nil, nil, errors.Wrap(err, "sessionstorage.AuthStore.LiveMailLinks()")
	}
	for _, link := range links {
		if link.InvitedByUserID.Valid || link.TenantID != "" || link.NewTenant != nil || !slices.Contains(link.RoleNames, superAdministratorRole) {
			continue
		}
		inv := invitationOf(link)
		switch {
		case email == "" || !sameAddress(inv.Email, email) || !slices.Equal(distinctRoleNames(inv.RoleNames), roles):
			stale = append(stale, inv)
		case standing == nil:
			standing = inv
		case inv.ExpiresAt.After(standing.ExpiresAt):
			stale = append(stale, standing)
			standing = inv
		default:
			stale = append(stale, inv)
		}
	}

	return standing, stale, nil
}

// withdrawFirstAdministrator withdraws inv, while nobody holds the Super Administrator
// role, and notes its address on result. An invitation that no longer stands was
// accepted or ran out since it was read: accepted reports that somebody holds the role
// now, which ends the call, since the first administrator is in.
func (p *AuthAPI[S, U, Tx]) withdrawFirstAdministrator(ctx context.Context, holders RoleHolders, inv *sessioninfo.Invitation, result *FirstAdministrator) (accepted bool, err error) {
	gone, err := p.revokeFirstAdministrator(ctx, inv, result)
	if err != nil || !gone {
		return false, err
	}

	return p.superAdministratorHeld(ctx, holders)
}

// revokeFirstAdministrator withdraws inv and notes its address on result. gone reports
// an invitation that no longer stood, accepted or run out since it was read, which is
// no error and withdraws nothing.
func (p *AuthAPI[S, U, Tx]) revokeFirstAdministrator(ctx context.Context, inv *sessioninfo.Invitation, result *FirstAdministrator) (gone bool, err error) {
	err = p.auth.storage.RevokeMailLink(ctx, inv.ID)
	switch {
	case err == nil:
		result.Withdrawn = append(result.Withdrawn, inv.Email)

		return false, nil
	case httpio.HasNotFound(err):
		return true, nil
	default:
		return false, errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()")
	}
}

// withdrawnBefore says, on a call that ends with err, which invitations it withdrew
// first: their links are dead although no new one was mailed.
func withdrawnBefore(err error, withdrawn []string) error {
	if len(withdrawn) == 0 {
		return err
	}

	return errors.Wrapf(err, "the invitation mailed to %s was withdrawn and no new one was sent", strings.Join(withdrawn, ", "))
}

// distinctRoleNames is the role names an invitation carries as two invitations are
// compared by them: each once, in order.
func distinctRoleNames(names []string) []string {
	distinct := slices.Clone(names)
	slices.Sort(distinct)

	return slices.Compact(distinct)
}
