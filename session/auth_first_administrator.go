package session

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/mail"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// The first administrator of a deployed application comes in as everyone after them
// does: by an invitation mailed to their own address, which they accept by signing in
// through a method of the Auth. The deployment names the address, and its release asks
// for the invitation (InviteFirstAdministrator) while the Auth has no account. No
// account is created ahead of the person and no password is generated for them, so
// nothing stands that somebody other than the mailbox's owner could sign in with.
//
// While the Auth has no account nobody is signed in, so an invitation into the
// application that no user sent is the deployment's own: those are the invitations this
// file reads and withdraws, and it touches no other. An invitation into a tenant, one
// that makes a tenant and one a user sent are never the first administrator's.
//
// Nothing here locks: a deployment runs one release at a time, and the step is its.

// The refusals of the first administrator's invitation. Each says what is wrong and
// what to do; the caller says which Auth and which setting it read.
var (
	// ErrFirstAdministratorUnnamed refuses an invitation with no address to mail it to.
	ErrFirstAdministratorUnnamed = errors.New("no first administrator is named: the application has no account yet, and its first administrator " +
		"comes in by an invitation mailed to the address the deployment names. Name the person's email address for this environment, then run this step again")
	// ErrFirstAdministratorNoMailbox refuses an invitation the Auth could only write to
	// a log: whoever opens an invitation's link accepts it, so the link reaches the
	// mailbox it was sent to and nothing else.
	ErrFirstAdministratorNoMailbox = errors.New("the application has no mailbox to send as, so the first administrator's invitation was not sent: " +
		"mail is written to the log here, and an invitation's link is never written to a log, since whoever opens it becomes the administrator. " +
		"Give this environment its mailbox, then run this step again")
	// ErrFirstAdministratorHasAccount refuses to send the invitation again once an
	// account exists.
	ErrFirstAdministratorHasAccount = errors.New("the application has an account, so no first administrator is invited: " +
		"the invitation is sent again only until somebody has an account. A person who has one invites the others from the application")
)

// FirstAdministratorRequest names the person a deployment invites as the Auth's first
// administrator, and what the invitation gives them.
type FirstAdministratorRequest struct {
	// Email is the address the deployment names for the person, where the invitation
	// is mailed.
	Email string
	// RoleNames are the roles the acceptance gives, as the application's acceptance
	// hook understands them (SetInvitationAccepted): at least one.
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
	// FirstAdministratorHasAccount says the Auth has an account: nobody is invited,
	// and nothing was read or changed beyond that.
	FirstAdministratorHasAccount
)

// FirstAdministrator is what a call did about the first administrator's invitation. It
// never carries the invitation's link or its token: those are in the message alone.
type FirstAdministrator struct {
	// Outcome says what was done.
	Outcome FirstAdministratorOutcome
	// Email is the address the invitation that stands after the call was mailed to,
	// bare, and ExpiresAt when it stops being accepted; both zero when the Auth has an
	// account.
	Email     string
	ExpiresAt time.Time
	// Withdrawn are the addresses of the invitations the call withdrew, whose links no
	// longer open: one the deployment no longer names, one whose roles are no longer
	// the ones asked for, and the standing one a fresh invitation replaces.
	Withdrawn []string
}

// InviteFirstAdministrator is the step a release runs for the Auth's first
// administrator. While the Auth has no account it leaves exactly one invitation
// standing: the one mailed to req.Email with req.RoleNames.
//
//   - The Auth has an account: nothing is done (FirstAdministratorHasAccount). Every
//     person after the first is invited from the application.
//   - An invitation to the address with those roles stands: nothing is mailed
//     (FirstAdministratorStanding), so a release while the person has not yet accepted
//     sends no second message.
//   - None stands, because none was sent or the one sent has run out
//     (MailSettings.InvitationTimeout, seven days unless set): one is mailed
//     (FirstAdministratorInvited).
//   - One stands that the deployment no longer names (another address, or other roles):
//     it is withdrawn, its link no longer opens, and the address named now is mailed.
//
// It refuses, with nothing mailed, an empty address (ErrFirstAdministratorUnnamed) and
// an Auth whose sender delivers nothing (ErrFirstAdministratorNoMailbox, mail.Delivers).
// An invitation the deployment no longer names is withdrawn before either refusal, so a
// refused run never leaves a link standing for an address the deployment took back, and
// the error says which were withdrawn. The checks every invitation is held to apply to
// the address too (Invite): one no sign-in method of the Auth could admit is refused,
// and a message that could not be sent leaves no invitation standing.
func (p *AuthAPI[S, U, Tx]) InviteFirstAdministrator(ctx context.Context, req *FirstAdministratorRequest) (*FirstAdministrator, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return p.inviteFirstAdministrator(ctx, req, false)
}

// ReinviteFirstAdministrator withdraws the first administrator's standing invitation
// and mails a fresh one to req.Email, for a message that was lost: the earlier link no
// longer opens. It is InviteFirstAdministrator in every other respect, except that an
// Auth with an account is refused (ErrFirstAdministratorHasAccount): there is somebody
// to invite the others, and nothing is withdrawn. The standing invitation is left as it
// is when the fresh one cannot be mailed for want of a mailbox, so the refusal takes
// nothing away.
func (p *AuthAPI[S, U, Tx]) ReinviteFirstAdministrator(ctx context.Context, req *FirstAdministratorRequest) (*FirstAdministrator, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	result, err := p.inviteFirstAdministrator(ctx, req, true)
	if err != nil {
		return nil, err
	}
	if result.Outcome == FirstAdministratorHasAccount {
		return nil, ErrFirstAdministratorHasAccount
	}

	return result, nil
}

// inviteFirstAdministrator is InviteFirstAdministrator, and with fresh
// ReinviteFirstAdministrator, which replaces the standing invitation too.
func (p *AuthAPI[S, U, Tx]) inviteFirstAdministrator(ctx context.Context, req *FirstAdministratorRequest, fresh bool) (*FirstAdministrator, error) {
	roles := distinctRoleNames(req.RoleNames)
	if len(roles) == 0 {
		return nil, errors.New("the first administrator's invitation names no role: an invitation gives what it names, and a person it gave nothing could administer nothing")
	}
	hasAccount, err := p.hasAccount(ctx)
	if err != nil {
		return nil, err
	}
	if hasAccount {
		return &FirstAdministrator{Outcome: FirstAdministratorHasAccount}, nil
	}

	// The address as an invitation keeps it, bare, so one written with a display name is
	// the same address at the next release. One that is no address names nobody: nothing
	// standing is the deployment's any more, and the refusal follows the withdrawals.
	email, bare, notAddress := strings.TrimSpace(req.Email), "", error(nil)
	if email != "" {
		bare, notAddress = address(email)
	}
	standing, stale, err := p.firstAdministratorInvitations(ctx, bare, roles)
	if err != nil {
		return nil, err
	}
	result := &FirstAdministrator{}
	for _, inv := range stale {
		accepted, err := p.withdrawFirstAdministrator(ctx, inv, result)
		if err != nil {
			return nil, err
		}
		if accepted {
			return &FirstAdministrator{Outcome: FirstAdministratorHasAccount, Withdrawn: result.Withdrawn}, nil
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

	m, err := p.auth.mailOf()
	if err != nil {
		return nil, err
	}
	if !mail.Delivers(m.sender) {
		return nil, withdrawnBefore(ErrFirstAdministratorNoMailbox, result.Withdrawn)
	}
	if standing != nil {
		accepted, err := p.withdrawFirstAdministrator(ctx, standing, result)
		if err != nil {
			return nil, err
		}
		if accepted {
			return &FirstAdministrator{Outcome: FirstAdministratorHasAccount, Withdrawn: result.Withdrawn}, nil
		}
	}
	invitation, err := p.Invite(ctx, &InvitationRequest{Email: bare, RoleNames: roles})
	if err != nil {
		return nil, withdrawnBefore(err, result.Withdrawn)
	}
	result.Outcome, result.Email, result.ExpiresAt = FirstAdministratorInvited, invitation.Email, invitation.ExpiresAt

	return result, nil
}

// hasAccount reports whether the Auth has an account: a user of any kind, signed in or
// not, disabled or not.
func (p *AuthAPI[S, U, Tx]) hasAccount(ctx context.Context) (bool, error) {
	users, err := p.auth.storage.Users(ctx)
	if err != nil {
		return false, errors.Wrap(err, "sessionstorage.AuthStore.Users()")
	}

	return len(users) > 0, nil
}

// firstAdministratorInvitations reads the deployment's own standing invitations (an
// invitation into the application that no user sent) and tells them apart: standing is
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
		if link.InvitedByUserID.Valid || link.TenantID != "" || link.NewTenant != nil {
			continue
		}
		inv := invitationOf(link)
		switch {
		case email == "" || !strings.EqualFold(inv.Email, email) || !slices.Equal(distinctRoleNames(inv.RoleNames), roles):
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

// withdrawFirstAdministrator withdraws inv and notes its address on result. An
// invitation that no longer stands was accepted or ran out since it was read: accepted
// reports that the Auth has an account now, which ends the call, since the first
// administrator is in.
func (p *AuthAPI[S, U, Tx]) withdrawFirstAdministrator(ctx context.Context, inv *sessioninfo.Invitation, result *FirstAdministrator) (accepted bool, err error) {
	err = p.auth.storage.RevokeMailLink(ctx, inv.ID)
	switch {
	case err == nil:
		result.Withdrawn = append(result.Withdrawn, inv.Email)

		return false, nil
	case !httpio.HasNotFound(err):
		return false, errors.Wrap(err, "sessionstorage.AuthStore.RevokeMailLink()")
	}

	return p.hasAccount(ctx)
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
