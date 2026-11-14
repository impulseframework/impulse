// Package signinflow establishes the session of an Auth sign-in over a storage driver:
// it resolves the identity's user, decides the sign-in and inserts the session, once
// for every driver, generic over the driver's transaction type. The drivers keep the
// SQL: each supplies the primitives, every one taking the transaction the flow opened.
package signinflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// Driver is what the flow needs from a storage driver. Every primitive runs inside a
// transaction Transact opened and receives it.
type Driver[Tx any] interface {
	// Transact runs fn in a read-write transaction and commits when fn returns nil.
	// Spanner retries an aborted transaction, so fn may run more than once and starts
	// from its inputs on every run.
	Transact(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
	// IdentityIn returns the link for (method, connection, subject), or nil when the
	// identity is not linked.
	IdentityIn(ctx context.Context, tx Tx, method sessioninfo.AuthMethod, connection, subject string) (*dbtype.SessionIdentity, error)
	// TouchIdentityIn moves a link's LastUsedAt to the database's time at commit.
	TouchIdentityIn(ctx context.Context, tx Tx, linkID impulse.UUID) error
	// LinkIdentityIn writes a new link of identity to userID, created and last used at
	// the database's time at commit. A link of the same identity made meanwhile ends the
	// transaction with an IdentityConflict.
	LinkIdentityIn(ctx context.Context, tx Tx, userID impulse.UUID, identity *sessioninfo.Identity, tenant string) error
	// IdentitiesByUserIn lists userID's links inside tx, oldest first.
	IdentitiesByUserIn(ctx context.Context, tx Tx, userID impulse.UUID) ([]*dbtype.SessionIdentity, error)
	// UserIn reads the part of userID's user the flow decides on. A missing user is
	// NotFound.
	UserIn(ctx context.Context, tx Tx, userID impulse.UUID) (*dbtype.User, error)
	// LockUserIn reads the user as UserIn does and holds the user against another link
	// decision until tx ends, so the check of the user's links and the new link are
	// not raced; a session insert for the user, and an account rule's own reads of the
	// user, are not held. A missing user is NotFound.
	LockUserIn(ctx context.Context, tx Tx, userID impulse.UUID) (*dbtype.User, error)
	// UserByUsernameIn reads the user whose username is username, matched without
	// regard to case as the users table's normalized username is, or nil when there is
	// none.
	UserByUsernameIn(ctx context.Context, tx Tx, username string) (*dbtype.User, error)
	// UserByVerifiedEmailIn reads the user who holds email as a verified address,
	// matched without regard to case as the users table's normalized address is, or
	// nil when there is none; an address a user holds unverified matches no one.
	UserByVerifiedEmailIn(ctx context.Context, tx Tx, email string) (*dbtype.User, error)
	// InsertUserIn writes a new user row and returns its ID. A taken username ends the
	// transaction with a UsernameConflict.
	InsertUserIn(ctx context.Context, tx Tx, user *dbtype.InsertSessionUser) (impulse.UUID, error)
	// InsertSessionIn writes session row id for req with its auth events and its custom
	// session data: req.CustomData, or what the configured resolver returns.
	InsertSessionIn(ctx context.Context, tx Tx, id impulse.UUID, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest) error
	// SignInConflict classifies the error a transaction ended with.
	SignInConflict(err error) dbtype.SignInConflict
	// ConsumeMailLinkIn consumes the live mail link id inside tx, at the database's
	// time at commit. A link that is not live is NotFound, and the transaction may go
	// on.
	ConsumeMailLinkIn(ctx context.Context, tx Tx, id impulse.UUID, now time.Time) error
	// ClaimVerifiedEmailIn writes email on the user as verified inside tx when the
	// user holds no verified address yet and no other user holds email, and reports
	// whether it wrote.
	ClaimVerifiedEmailIn(ctx context.Context, tx Tx, userID impulse.UUID, email string) (bool, error)
	// UserByEmailIn reads the user holding email inside tx, verified or not, matched
	// without regard to case, or nil when there is none.
	UserByEmailIn(ctx context.Context, tx Tx, email string) (*dbtype.User, error)
	// UsernameIsEmail reports whether the store's usernames are the addresses.
	UsernameIsEmail() bool
}

// InvitationAcceptor is the application's half of an invitation's acceptance: it
// gives userID what the invitation names (its roles, in its tenant), inside the
// transaction that consumes the invitation, which it is handed: the invitation is
// spent and its roles given together, or neither. It reads and writes through tx
// alone: the Spanner emulator allows one read-write transaction at a time, so a second
// transaction or a read through another client inside the hook waits on the one it
// runs in. A user the same transaction provisioned is buffered and not readable in it
// (Spanner applies a buffered row at the commit), so the user is named by id and by
// username, which is what the hook writes under.
//
// committed, nil for none, is work the flow runs once that transaction has committed
// and before the sign-in policy decides: telling a permission engine that its rows
// changed, so the policy sees the membership. A transaction's closure may run more
// than once (Spanner retries an aborted transaction), so the hook is called once per
// run and only the committed function of the run that committed is called, once. An
// error fails the transaction: the invitation stands, a user the transaction would
// have provisioned is not created, and the sign-in ends with the error.
type InvitationAcceptor[Tx any] func(ctx context.Context, tx Tx, userID impulse.UUID, username string, inv *sessioninfo.Invitation) (committed func(ctx context.Context), err error)

// DirectorySeater is the application's half of a directory's default seat: it seats
// userID, named by id and by username, with seats, the default seats of the
// directories the tenants declared for the identity's connection, inside the
// resolution's transaction that provisions the user, which it is handed: the account
// is made and seated together, or neither. It is held to what InvitationAcceptor is:
// tx alone for reads and writes, a committed function (nil for none) the flow runs
// once the transaction has committed and before the sign-in policy decides, and an
// error that fails the transaction, so no account is left holding nothing.
type DirectorySeater[Tx any] func(ctx context.Context, tx Tx, userID impulse.UUID, username string, seats []signin.DirectorySeat) (committed func(ctx context.Context), err error)

// TrustedDeviceCheck reports whether the browser signing in is a trusted device of
// userID: a device the person asked to be remembered after a second factor, named by
// the trusted device cookie and live in the trusted devices table. The Auth supplies
// it with the cookie's token on the context, and it answers at once, without a read,
// for a browser that carries none. It is asked once the user is known and before the
// decision's transaction opens, since it reads the store on its own (Spanner refuses
// a read inside another transaction), and its yes records a trusted-device event on
// the session in the step's place when the sign-in policy holds the sign-in for MFA
// and admits a trusted device.
type TrustedDeviceCheck func(ctx context.Context, userID impulse.UUID) (bool, error)

// Rules are the application's rules a sign-in runs under.
type Rules[Tx any] struct {
	// Account is the registration's own account rule, nil for none, and Standard the
	// registration's standard rule, "" for none; see SignIn.
	Account  signin.AccountRule[Tx]
	Standard signin.StandardAccountRule
	// Policy is the sign-in policy, nil for none.
	Policy signin.Policy[Tx]
	// Accept is the application's invitation acceptor, nil for none.
	Accept InvitationAcceptor[Tx]
	// Directories reads the tenants' declarations of a directory for a stranger's
	// first sign-in, nil for none; Roles is the role store the person's memberships
	// are read from, nil for none, and Seat seats the person with the default seats
	// the declarations name, nil for none; see standardRule.
	Directories signin.DirectoryReader[Tx]
	Roles       signin.UserRoleManager
	Seat        DirectorySeater[Tx]
	// TrustedDevice reports whether the browser is a trusted device of the user, nil
	// for none; see decideSignIn.
	TrustedDevice TrustedDeviceCheck
}

// SignIn establishes session id for a request that carries a verified identity, in two
// transactions (see the README's "Storage and schema"). The first resolves an external
// identity's user: a linked identity is its user's, and an unlinked one goes to the
// account rules: the registration's custom rule when it has one, and the standard rule
// when it has none or the custom rule answers signin.Standard. The second reads the
// user, asks policy and inserts the session. Each transaction commits unless a hook or
// the driver fails, so a refusal or a pending outcome keeps the hooks' own writes and
// writes no session. The resolution commits first because Spanner hides buffered
// writes from reads in the same transaction: the policy and the custom session data
// resolver must see a provisioned user and the rows OnProvisioned wrote.
//
// A password identity names its user in req.UserID and skips the resolution. Once the
// user is known, req.UserID, req.Username and req.User are set to it, whatever the
// outcome. A refusal is a *sessioninfo.LoginRefusal and a sign-in that must wait a
// *signin.PendingSignInError.
//
// A sign-in that carries an invitation (req.Invitation) accepts it once the user is
// known and before the policy decides, so the policy sees what the acceptance
// granted. It is accepted only as the invited address: a sign-in whose address is not
// the one the invitation was mailed to, the directory's for an external identity and
// the account's own for a password one, is refused before anything is read or written
// (invitation_address), the invitation left standing for the invited account. An
// external identity that asserts no address cannot match; a password account that
// holds no address accepts by the link, since the link is what the person proved, and
// takes the address. Accepting it consumes the invitation and sets the user's address from it when the
// user holds no verified address, in the resolution transaction for an external
// identity (so a user the invitation provisioned and the consumption commit together)
// and in a transaction of its own for a password identity; rules.Accept gives what
// the invitation names inside that same transaction, which it is handed, so the
// invitation is spent and its roles given together or neither is. The
// invitation is consumed only once the resolution has admitted the sign-in: the user
// found, linked or provisioned, and enabled. A sign-in the account rules refuse or
// hold for a confirmation, or a disabled user's, leaves the invitation standing,
// nothing granted, so the person may sign in another way; a sign-in the policy then
// denies leaves it accepted, and what the acceptance granted stands. An invitation
// that is no longer live is skipped and the sign-in goes on, unless the invitation
// alone admitted the person (the invited-users-only rule), which is then a refusal
// (invitation_expired) and provisions nothing; req.Invitation.Accepted reports whether
// it was accepted. An acceptor that fails ends the sign-in with its error and fails
// the transaction: the invitation stands, a user the resolution would have provisioned
// or linked is not, and Accepted is false.
//
// A stranger from a directory some tenant declares is decided by the tenants' rows
// when rules.Directories is set (standardRule): a user the default seats provisioned
// is seated by rules.Seat inside the resolution's transaction, before the invitation's
// grant, so the account is made and seated together or neither is.
//
// What the two hooks ask to have run once their transaction has committed (their
// committed functions: the word to a permission engine that its rows changed) runs
// right after the commit and before the policy decides, so the policy sees the
// memberships. Only the functions of the run of the transaction that committed are
// called, each once.
func SignIn[Tx any](
	ctx context.Context, d Driver[Tx], id impulse.UUID, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) error {
	if req.Identity == nil {
		return errors.New("a sign-in carries its verified identity in req.Identity")
	}

	resolved := *req
	resolved.User = nil
	defer func() { req.UserID, req.Username, req.User = resolved.UserID, resolved.Username, resolved.User }()

	if stop := invitedAddressRefusal(req); stop != nil {
		return stop
	}
	source, tenant := sessioninfo.UserNamed, ""
	switch {
	case dbtype.IsExternal(req.Identity):
		if rules.Account == nil && rules.Standard == "" {
			return errors.Newf("the %s sign-in method has neither a standard account rule nor a custom one", req.Identity.Method)
		}
		var (
			committed committedWork
			stop      error
			err       error
		)
		source, tenant, committed, stop, err = resolveIdentity(ctx, d, &resolved, rules)
		if err != nil {
			return err
		}
		// The resolution has committed with the default seats and the invitation's
		// roles in it: what the hooks asked to have run after the commit runs now,
		// before the policy reads the memberships.
		committed.run(ctx)
		if stop != nil {
			return stop
		}
		// A link or a provisioned user is reported even when the decision then fails.
		resolved.User = &sessioninfo.SignInUser{ID: resolved.UserID, Username: resolved.Username, Source: source, Tenant: tenant}
	case req.UserID.IsNil():
		return errors.New("a password sign-in names its user in req.UserID")
	default:
		if req.Invitation != nil {
			// The address was held to the invited one above. A dead invitation is
			// skipped and the sign-in goes on; an acceptor that fails ends it, the
			// invitation standing.
			if _, err := acceptInvitation(ctx, d, resolved.UserID, resolved.Username, req.Invitation, rules.Accept); err != nil {
				return err
			}
		}
	}

	trusted, err := trustedDevice(ctx, resolved.UserID, rules.TrustedDevice)
	if err != nil {
		return err
	}

	return decideAndInsert(ctx, d, id, insertSession, &resolved, source, tenant, rules.Policy, trusted)
}

// invitedAddressRefusal refuses a sign-in that carries an invitation mailed to another
// address: the address the sign-in asserts (Identity.Email: the directory's for an
// external identity, the account's own for a password one) must be the invited one, so
// an invitation is accepted only by the account it was sent to, whoever holds the link
// (addressRefusal). Nothing is read or written: the invitation stands and the login
// page, which still carries it, names the invited address. Nil when the sign-in
// carries no invitation or the acceptance may go on.
func invitedAddressRefusal(req *sessioninfo.NewSessionRequest) error {
	if req.Invitation == nil {
		return nil
	}

	return addressRefusal(req.Identity.Email, !dbtype.IsExternal(req.Identity), req.Invitation)
}

// addressRefusal is the refusal (invitation_address) of an acceptance of inv by an
// account whose address, email, is not the invited one, compared without regard to
// case and surrounding space. An account with no address ("") may accept when
// linkSuffices says so: a password account or a person signed in, whose only proof is
// the link itself and who takes the invited address by accepting; a directory
// identity that asserts no address cannot match. Nil when the acceptance may go on.
func addressRefusal(email string, linkSuffices bool, inv *sessioninfo.Invitation) error {
	email = strings.TrimSpace(email)
	if email == "" && linkSuffices {
		return nil
	}
	if sameAddress(email, inv.Email) {
		return nil
	}

	return Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, InvitedAddressMessage(inv))
}

// InvitedAddressMessage is the sentence shown for an acceptance refused for another
// address: the invited address, and that the person signs in with that account.
func InvitedAddressMessage(inv *sessioninfo.Invitation) string {
	return fmt.Sprintf("This invitation is for %s. Sign in with that account.", inv.Email)
}

// sameAddress reports whether two addresses are one, without regard to case and
// surrounding space; an empty address is nobody's and matches nothing.
func sameAddress(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)

	return a != "" && strings.EqualFold(a, b)
}

// trustedDevice asks check, nil for none, whether the browser is a trusted device of
// userID, outside any transaction.
func trustedDevice(ctx context.Context, userID impulse.UUID, check TrustedDeviceCheck) (bool, error) {
	if check == nil {
		return false, nil
	}
	ok, err := check(ctx, userID)
	if err != nil {
		return false, errors.Wrap(err, "TrustedDeviceCheck()")
	}

	return ok, nil
}

// errInvitationDead ends the resolution transaction of a sign-in that its invitation
// alone admitted, once the invitation turns out to be dead: nothing is committed.
var errInvitationDead = errors.New("the invitation has expired or was already used")

// consumeInvitation consumes the invitation req carries, if any, inside the
// resolution's transaction once it has admitted the sign-in (for a provisioned user,
// before the row is written), and reports whether it was live. A dead invitation is
// reported dead and the sign-in goes on without it, unless it alone admitted the
// person (byInvitation), which ends the transaction with errInvitationDead.
func consumeInvitation[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, byInvitation bool) (live bool, err error) {
	inv := req.Invitation
	if inv == nil {
		return false, nil
	}
	inv.Accepted = false
	if err := d.ConsumeMailLinkIn(ctx, tx, inv.ID, time.Now()); err != nil {
		if !httpio.HasNotFound(err) {
			return false, errors.Wrap(err, "Driver.ConsumeMailLinkIn()")
		}
		if byInvitation {
			return false, errInvitationDead
		}

		return false, nil
	}

	return true, nil
}

// claimInvitation finishes the consumption of the live invitation req carries for the
// user the resolution found, inside its transaction: the user's address is set from
// it when the user holds no verified address (a user provisioned in this transaction
// had it set at the insert).
func claimInvitation[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, source sessioninfo.UserSource) error {
	if source != sessioninfo.UserProvisioned {
		if _, err := d.ClaimVerifiedEmailIn(ctx, tx, req.UserID, req.Invitation.Email); err != nil {
			return errors.Wrap(err, "Driver.ClaimVerifiedEmailIn()")
		}
	}

	return nil
}

// committedWork is what the hooks of one run of a transaction's closure asked to have
// run once the transaction has committed, in the order they asked. A closure that runs
// again starts an empty one, so the work of a run that did not commit is never run.
type committedWork []func(ctx context.Context)

// add notes f, nil for none.
func (w *committedWork) add(f func(ctx context.Context)) {
	if f != nil {
		*w = append(*w, f)
	}
}

// run calls the work, once the transaction it was collected in has committed.
func (w committedWork) run(ctx context.Context) {
	for _, f := range w {
		f(ctx)
	}
}

// grantConsumed runs the application's acceptor, nil for none, for the live invitation
// the transaction consumed, inside that transaction, and answers what the acceptor
// asks to have run once it has committed. A failed grant is the acceptor's error and
// fails the transaction: the invitation stands.
func grantConsumed[Tx any](
	ctx context.Context, tx Tx, userID impulse.UUID, username string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx],
) (committed func(ctx context.Context), err error) {
	if accept == nil {
		return nil, nil
	}
	committed, err = accept(ctx, tx, userID, username, inv)
	if err != nil {
		return nil, errors.Wrap(err, "InvitationAcceptor()")
	}

	return committed, nil
}

// seatsBeyondInvitation leaves out the default seats the invitation this sign-in consumes
// also names: the same role in the same tenant. Both are given in the one transaction,
// by two hooks neither of which can read what the other buffered, and a row written
// twice in a transaction is refused at its commit, so the sign-in of a person invited
// with their directory's default role would fail every time. Such a role is given once,
// by the invitation. An invitation into the application names no tenant and gives its
// roles elsewhere (globally, or in every tenant), so it shares no row with a seat.
func seatsBeyondInvitation(seats []signin.DirectorySeat, inv *sessioninfo.Invitation) []signin.DirectorySeat {
	if inv == nil || inv.Tenant == "" || len(seats) == 0 {
		return seats
	}

	return slices.DeleteFunc(slices.Clone(seats), func(seat signin.DirectorySeat) bool {
		return string(seat.Tenant) == inv.Tenant && slices.Contains(inv.RoleNames, string(seat.Role))
	})
}

// seatDeclared grants the default seats a resolution carries, inside its transaction:
// seat, handed the transaction, and answers what it asks to have run once the
// transaction has committed. Seats on an Auth with no seater is a wiring mistake, a
// directory reader set without its hook, and fails the transaction as a seater's error
// does: no account is made.
func seatDeclared[Tx any](
	ctx context.Context, tx Tx, userID impulse.UUID, username string, seats []signin.DirectorySeat, seat DirectorySeater[Tx],
) (committed func(ctx context.Context), err error) {
	if len(seats) == 0 {
		return nil, nil
	}
	if seat == nil {
		return nil, errors.New("the resolution grants default seats and the Auth has no directory seat hook: set both with SetDirectoryAdmission")
	}
	committed, err = seat(ctx, tx, userID, username, seats)
	if err != nil {
		return nil, errors.Wrap(err, "DirectorySeater()")
	}

	return committed, nil
}

// AcceptInvitation accepts inv for userID, whose address is email ("" for none), in
// one transaction: the invitation is consumed, the user's address is set from it when
// the user holds no verified address and no other user holds it, and accept, nil for
// none, gives what the invitation names, handed the transaction; what accept asks to
// have run after the commit runs once it has landed. inv.Accepted is set to the
// outcome. The user accepts only an invitation to their own address: one mailed to
// another address is refused (a *sessioninfo.LoginRefusal, invitation_address) with
// nothing written, the invitation left standing (addressRefusal); a user with no
// address accepts by the link and takes the address. An invitation that is no longer
// live (consumed, expired or revoked) is NotFound with signin.ErrInvitationExpired as
// its cause, and nothing is written; a grant that fails is the acceptor's error,
// whatever it is, with nothing written either: the invitation stands.
func AcceptInvitation[Tx any](ctx context.Context, d Driver[Tx], userID impulse.UUID, username, email string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx]) error {
	inv.Accepted = false
	if stop := addressRefusal(email, true, inv); stop != nil {
		return stop
	}
	dead, err := acceptInvitation(ctx, d, userID, username, inv, accept)
	if err != nil {
		return err
	}
	if dead {
		return httpio.NewNotFoundMessageWithError(signin.ErrInvitationExpired, "the invitation has expired or was already used")
	}

	return nil
}

// acceptInvitation is the transaction of an acceptance outside a resolution: a password
// sign-in's, and a signed-in person's (AcceptInvitation). dead reports an invitation
// that is no longer live, with nothing written and no error; an error is the driver's
// or the acceptor's, and nothing is written either. inv.Accepted is set to the outcome.
func acceptInvitation[Tx any](
	ctx context.Context, d Driver[Tx], userID impulse.UUID, username string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx],
) (dead bool, err error) {
	inv.Accepted = false
	var committed committedWork
	err = d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts with nothing owed and the invitation taken for live.
		committed, dead = nil, false
		if err := d.ConsumeMailLinkIn(ctx, tx, inv.ID, time.Now()); err != nil {
			dead = httpio.HasNotFound(err)

			return errors.Wrap(err, "Driver.ConsumeMailLinkIn()")
		}
		if _, err := d.ClaimVerifiedEmailIn(ctx, tx, userID, inv.Email); err != nil {
			return errors.Wrap(err, "Driver.ClaimVerifiedEmailIn()")
		}
		granted, err := grantConsumed(ctx, tx, userID, username, inv, accept)
		if err != nil {
			return err
		}
		committed.add(granted)

		return nil
	})
	if err != nil {
		if dead {
			return true, nil
		}

		return false, errors.Wrap(err, "Driver.Transact()")
	}
	inv.Accepted = true
	committed.run(ctx)

	return false, nil
}

// resolveIdentity is the first transaction of SignIn. It sets req.UserID (and
// req.Username for a provisioned user) and reports how the user was found and the
// link's tenant. stop is a refusal or a pending confirmation; it still commits. When two
// first sign-ins of one identity race, the loser's link fails on the identities key (a
// provisioning loser on the username first), and its resolution runs once more and
// finds the winner's link. A transaction the database ends to break a deadlock or a
// serialization failure between racing sign-ins runs once more too.
//
// committed is what the hooks of the run that committed asked to have run after the
// commit, for the caller to run: every run of the closure, the database's own retries
// and the loop's alike, starts an empty one, so a run that did not commit leaves
// nothing to run. A transaction that fails, an acceptor's or a seater's error among
// the causes, leaves the invitation the sign-in carries unaccepted.
func resolveIdentity[Tx any](
	ctx context.Context, d Driver[Tx], req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (source sessioninfo.UserSource, tenant string, committed committedWork, stop, err error) {
	base := *req
	for attempt := 1; ; attempt++ {
		err = d.Transact(ctx, func(ctx context.Context, tx Tx) error {
			// Each run starts from the caller's request, with nothing owed.
			*req = base
			var err error
			source, tenant, committed, stop, err = resolveIn(ctx, d, tx, req, rules)

			return err
		})
		if err == nil {
			return source, tenant, committed, stop, nil
		}
		// Nothing was committed: the invitation was not accepted, whatever the run set.
		if req.Invitation != nil {
			req.Invitation.Accepted = false
		}
		if errors.Is(err, errInvitationDead) {
			return "", "", nil, Refusal("", sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired, "the invitation has expired or was already used"), nil
		}

		switch d.SignInConflict(err) {
		case dbtype.UsernameConflict:
			// A provisioning sign-in that loses a race for the same identity fails on
			// the username before it reaches the identities key: the winner's link is
			// there now, so the resolution runs once more and finds it. A username that
			// is simply taken is refused on the second run.
			if attempt == 1 {
				continue
			}

			return "", "", nil, nil, httpio.NewConflictMessagef("username %q already exists", req.Username)
		case dbtype.IdentityConflict:
			if attempt == 1 {
				continue
			}
		case dbtype.LockConflict:
			// The database broke a deadlock (a rule that shared the user row's lock
			// before the link decision locked it) or a serialization failure between
			// racing sign-ins: the loser runs once more and sees what the winner wrote.
			if attempt == 1 {
				continue
			}
		case dbtype.NoSignInConflict:
		}

		return "", "", nil, nil, errors.Wrap(err, "Driver.Transact()")
	}
}

// resolveIn is one run of the resolution inside tx. A linked identity resolves to its
// user, named on the request, and its LastUsedAt is touched. An unlinked one goes to
// the account rules, and their answer is applied. The invitation the sign-in carries
// is consumed for the user found, in the same transaction, once the resolution has
// admitted the sign-in and not for a disabled user, who is refused once the sign-in
// is decided. What the sign-in gives is given here too, by the application's hooks
// handed tx: the default seats of a user the resolution provisioned (rules.Seat), then
// what the consumed invitation names (rules.Accept), each written with the row that
// causes it or not at all, and a role both would give given once, by the invitation
// (seatsBeyondInvitation). committed is what the hooks ask to have run once tx has
// committed.
func resolveIn[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (source sessioninfo.UserSource, tenant string, committed committedWork, stop, err error) {
	link, err := d.IdentityIn(ctx, tx, req.Identity.Method, req.Identity.Connection, req.Identity.Subject)
	if err != nil {
		return "", "", nil, nil, errors.Wrap(err, "Driver.IdentityIn()")
	}
	var (
		live  bool
		seats []signin.DirectorySeat
	)
	if link != nil {
		user, err := d.UserIn(ctx, tx, link.UserID)
		if err != nil {
			return "", "", nil, nil, errors.Wrap(err, "Driver.UserIn()")
		}
		req.UserID, req.Username = link.UserID, user.Username
		if err := d.TouchIdentityIn(ctx, tx, link.ID); err != nil {
			return "", "", nil, nil, errors.Wrap(err, "Driver.TouchIdentityIn()")
		}
		if link.Tenant != nil {
			tenant = *link.Tenant
		}
		source = sessioninfo.UserExistingLink
		if !user.Disabled {
			if live, err = consumeInvitation(ctx, d, tx, req, false); err != nil {
				return "", "", nil, nil, err
			}
		}
	} else {
		resolved, stop, err := resolveUnlinked(ctx, d, tx, req, rules)
		if err != nil || stop != nil {
			return "", "", nil, stop, err
		}
		source, tenant, seats, live = resolved.source, resolved.tenant, resolved.seats, resolved.live
	}
	if live {
		seats = seatsBeyondInvitation(seats, req.Invitation)
	}
	seated, err := seatDeclared(ctx, tx, req.UserID, req.Username, seats, rules.Seat)
	if err != nil {
		return "", "", nil, nil, err
	}
	committed.add(seated)
	if live {
		if err := claimInvitation(ctx, d, tx, req, source); err != nil {
			return "", "", nil, nil, err
		}
		granted, err := grantConsumed(ctx, tx, req.UserID, req.Username, req.Invitation, rules.Accept)
		if err != nil {
			return "", "", nil, nil, err
		}
		committed.add(granted)
		// Consumed and granted for this user, once the transaction commits.
		req.Invitation.Accepted = true
	}

	return source, tenant, committed, nil, nil
}

// unlinkedUser is how an identity linked to no user resolved: the user's source and
// the tenant key for the link, the default seats a provisioned user is granted in the
// same transaction, and whether the invitation the sign-in carries is live.
type unlinkedUser struct {
	source sessioninfo.UserSource
	tenant string
	seats  []signin.DirectorySeat
	live   bool
}

// resolveUnlinked resolves an identity that is linked to no user: the account rules
// decide it and their answer is applied.
func resolveUnlinked[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (resolved unlinkedUser, stop, err error) {
	res, byInvitation, stop, err := decideAccount(ctx, d, tx, req, rules)
	if err != nil || stop != nil {
		return unlinkedUser{}, stop, err
	}
	source, tenant, live, stop, err := applyResolution(ctx, d, tx, req, res, byInvitation)
	if err != nil || stop != nil {
		return unlinkedUser{}, stop, err
	}
	resolved = unlinkedUser{source: source, tenant: tenant, live: live}
	if source == sessioninfo.UserProvisioned {
		resolved.seats = res.Seats()
	}

	return resolved, nil, nil
}

// decideAccount asks the account rules what to do with an unlinked identity: the
// custom rule when the registration has one, and the standard rule when it has none or
// the custom rule answers signin.Standard. A refusal the standard rule makes before any
// row is written is returned as stop. The standard rule's tenant key is the
// organization the method names, or the identity's connection when it names none,
// unless the custom rule's Standard names another. byInvitation reports that the
// invitation the sign-in carries is what admitted the person.
func decideAccount[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (res *signin.Resolution, byInvitation bool, stop, err error) {
	rule, standard := rules.Account, rules.Standard
	tenant := req.Identity.Organization
	if tenant == "" {
		tenant = req.Identity.Connection
	}
	if rule != nil {
		res, err = rule(ctx, tx, req)
		if err != nil {
			return nil, false, nil, errors.Wrap(err, "signin.AccountRule()")
		}
		if res == nil || res.Outcome() != signin.ApplyStandardRule {
			return res, false, nil, nil
		}
		if res.Tenant() != "" {
			tenant = res.Tenant()
		}
	}
	if standard == "" {
		return nil, false, nil, errors.Newf("the %s sign-in method has no standard account rule: its account rule must decide the identity itself", req.Identity.Method)
	}

	return standardRule(ctx, d, tx, req, standard, tenant, rules)
}

// standardRule applies the standard account rule to an unlinked identity, inside tx.
// Every standard rule refuses a guest of the directory, a token asserting no
// application role when the method synchronizes roles, and an identity without a
// sign-in name, before any row is written. A pre-created user under the sign-in name,
// matched without regard to case, or one who holds the identity's verified address
// (preCreatedUser), is confirmed with its password when it has one and linked when it
// has none (applyResolution refuses the link when such a user already signs in through
// another connection). A person no user was created for goes to the tenants'
// declarations of the directory first, when the application reads them
// (rules.Directories; see declaredRule), and otherwise is provisioned under the
// sign-in name, or refused by the invited-users-only rule unless the sign-in carries
// an invitation, which that rule takes as the account it asks for.
func standardRule[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rule signin.StandardAccountRule, tenant string, rules Rules[Tx],
) (res *signin.Resolution, byInvitation bool, stop, err error) {
	switch {
	case req.Identity.Guest:
		return nil, false, Refusal("", sessioninfo.RefusedGuestAccount, signin.ErrGuestAccount, "guest accounts are not members of the directory"), nil
	case req.RoleNames != nil && len(req.RoleNames) == 0:
		return nil, false, Refusal("", sessioninfo.RefusedNoRoles, signin.ErrNoRoles, "Unauthorized: user has no roles"), nil
	case req.Identity.SignInName == "":
		return nil, false, Refusal("", sessioninfo.RefusedNoEmail, signin.ErrNoSignInName, "the identity carries no sign-in name"), nil
	}

	user, err := preCreatedUser(ctx, d, tx, req.Identity)
	if err != nil {
		return nil, false, nil, err
	}
	switch {
	case user != nil && user.HasPassword:
		return signin.Confirm(user.ID, signin.Tenant(tenant)), false, nil, nil
	case user != nil:
		return signin.Link(user.ID, false, signin.Tenant(tenant)), false, nil, nil
	}

	if rules.Directories != nil && req.Identity.Method.ThroughDirectory() {
		declared, err := rules.Directories(ctx, tx, signin.Directory{Method: req.Identity.Method, Connection: req.Identity.Connection})
		if err != nil {
			return nil, false, nil, errors.Wrap(err, "signin.DirectoryReader()")
		}
		if len(declared) > 0 {
			return declaredRule(ctx, req, declared, tenant, rules.Roles)
		}
	}

	switch rule {
	case signin.MembersOfDirectory, signin.OpenSignUp:
		return signin.Provision(&signin.NewUser{Username: req.Identity.SignInName}, nil, signin.Tenant(tenant)), false, nil, nil
	case signin.InvitedUsersOnly:
		// The invitation the browser carries is the account the rule asks for: the
		// person was invited by address, and SignIn has already held the directory's
		// address to the invited one.
		if req.Invitation != nil {
			return signin.Provision(&signin.NewUser{Username: req.Identity.SignInName}, nil, signin.Tenant(tenant)), true, nil, nil
		}

		return nil, false, Refusal("", sessioninfo.RefusedNotInvited, signin.ErrNotInvited, "no account was created for this person"), nil
	default:
		return nil, false, nil, errors.Newf("unknown standard account rule %q", string(rule))
	}
}

// declaredRule decides a person no user was created for, from a directory some
// tenant declares: a declared directory belongs to its tenant, and the tenant's
// declaration decides before the registration's standard rule. A person who already
// holds a membership under the sign-in name, in a declaring tenant or as the
// application's staff, is that tenant's person, seated ahead of their first sign-in:
// they are provisioned under the sign-in name, with the default seats of the
// declaring tenants where they hold nothing. A tenant that names a default seat
// admits a stranger: they are provisioned and seated in every tenant that names one,
// in the resolution's transaction (Rules.Seat). When no tenant names a seat and the
// person holds nothing, the tenants admit their people by invitation only: the
// invitation the browser carries is the account, as under the invited-users-only
// rule, and without one the sign-in is refused (invitation_only) and nothing is
// written.
func declaredRule(
	ctx context.Context, req *sessioninfo.NewSessionRequest, declared []signin.DeclaredDirectory, tenant string, roles signin.UserRoleManager,
) (res *signin.Resolution, byInvitation bool, stop, err error) {
	member, held, err := heldIn(ctx, roles, req.Identity.SignInName, declared)
	if err != nil {
		return nil, false, nil, err
	}
	seats := make([]signin.DirectorySeat, 0, len(declared))
	for _, d := range declared {
		if d.DefaultSeat != "" && !held[d.Tenant] {
			seats = append(seats, signin.DirectorySeat{Tenant: d.Tenant, Role: d.DefaultSeat})
		}
	}
	newUser := &signin.NewUser{Username: req.Identity.SignInName}
	switch {
	case len(seats) > 0:
		return signin.Provision(newUser, nil, signin.Tenant(tenant), signin.Seats(seats...)), false, nil, nil
	case member:
		return signin.Provision(newUser, nil, signin.Tenant(tenant)), false, nil, nil
	case req.Invitation != nil:
		return signin.Provision(newUser, nil, signin.Tenant(tenant)), true, nil, nil
	default:
		return nil, false, Refusal("", sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly, "this organization signs people in by invitation: ask your administrator to invite you"), nil
	}
}

// heldIn reads the memberships username holds, from roles (none when roles is nil):
// member reports a role held in one of the declaring tenants or as the application's
// staff (the global partition or every tenant domain), and held names the declaring
// tenants where a role is held.
func heldIn(
	ctx context.Context, roles signin.UserRoleManager, username string, declared []signin.DeclaredDirectory,
) (member bool, held map[accesstypes.Domain]bool, err error) {
	held = make(map[accesstypes.Domain]bool, len(declared))
	if roles == nil {
		return false, held, nil
	}
	collection, err := roles.UserRoles(ctx, accesstypes.User(username))
	if err != nil {
		return false, nil, errors.Wrap(err, "signin.UserRoleManager.UserRoles()")
	}
	for scope, scopeRoles := range collection {
		if len(scopeRoles) == 0 {
			continue
		}
		if scope.IsGlobal() || scope.IsEveryDomain() {
			member = true

			continue
		}
		domain, ok := scope.Domain()
		if !ok {
			continue
		}
		for _, d := range declared {
			if d.Tenant == domain {
				member, held[domain] = true, true
			}
		}
	}

	return member, held, nil
}

// preCreatedUser finds the user an unlinked identity belongs to, before any row is
// written: the user under the identity's sign-in name, matched without regard to case,
// or else the user who holds the identity's address verified, when the method verified
// the address (Identity.EmailVerified; Google does, Entra does not). A person is one
// user whichever way they sign in, and an address counts once the person has proven
// it: an address the method did not verify, or one a user holds unverified, matches no
// one. nil when there is neither.
func preCreatedUser[Tx any](ctx context.Context, d Driver[Tx], tx Tx, identity *sessioninfo.Identity) (*dbtype.User, error) {
	user, err := d.UserByUsernameIn(ctx, tx, identity.SignInName)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByUsernameIn()")
	}
	if user != nil || !identity.EmailVerified || identity.Email == "" {
		return user, nil
	}
	user, err = d.UserByVerifiedEmailIn(ctx, tx, identity.Email)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByVerifiedEmailIn()")
	}

	return user, nil
}

// applyResolution acts on the account rules' answer for an unlinked identity. A link
// or a new user sets req.UserID and req.Username and reports how the user was found
// and its tenant, and consumes the invitation the sign-in carries, if any, once the
// user is linked or about to be written (live reports that it was): a refusal or a
// pending confirmation, returned as stop, consumes nothing, and neither does a
// disabled user, who is refused once the sign-in is decided. byInvitation says the
// invitation alone admitted the person, so a dead one is a refusal.
func applyResolution[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, res *signin.Resolution, byInvitation bool,
) (source sessioninfo.UserSource, tenant string, live bool, stop, err error) {
	if res == nil {
		res = signin.Reject("")
	}

	switch res.Outcome() {
	case signin.RejectIdentity:
		return "", "", false, Refusal(res.Refusal(), sessioninfo.RefusedIdentityRejected, signin.ErrIdentityRejected, "identity rejected"), nil
	case signin.LinkIdentity:
		live, stop, err = linkUser(ctx, d, tx, req, res)
		if err != nil || stop != nil {
			return "", "", false, stop, err
		}

		return sessioninfo.UserNewLink, res.Tenant(), live, nil, nil
	case signin.ProvisionUser:
		live, stop, err = provisionUser(ctx, d, tx, req, res, byInvitation)
		if err != nil || stop != nil {
			return "", "", false, stop, err
		}

		return sessioninfo.UserProvisioned, res.Tenant(), live, nil, nil
	case signin.RequireConfirmation:
		user, err := d.UserIn(ctx, tx, res.UserID())
		if err != nil {
			return "", "", false, nil, errors.Wrap(err, "Driver.UserIn()")
		}

		return "", "", false, &signin.PendingSignInError{
			Reason: sessioninfo.PendingConfirmation, UserID: impulse.NullUUIDFromUUID(res.UserID()), Username: user.Username, Tenant: res.Tenant(),
		}, nil
	case signin.ApplyStandardRule:
		return "", "", false, nil, errors.New("the standard account rule answered signin.Standard: a standard rule decides the identity itself")
	default:
		return "", "", false, nil, errors.Newf("unknown account rule outcome %d", res.Outcome())
	}
}

// linkUser links the identity to the user the rule named (signin.LinkIdentity), names
// the user on the request, and consumes the invitation the sign-in carries unless the
// user is disabled. An untrusted link to a user with a password, or from a connection
// a password-less user does not sign in through, is a refusal (stop) and links
// nothing.
func linkUser[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, res *signin.Resolution) (live bool, stop, err error) {
	// The row is held until the transaction ends, so two link decisions for one
	// user run one after the other and the second sees the first's link.
	user, err := d.LockUserIn(ctx, tx, res.UserID())
	if err != nil {
		return false, nil, errors.Wrap(err, "Driver.LockUserIn()")
	}
	if user.HasPassword && !res.Trusted() {
		return false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkRequiresConfirmation, "identity requires confirmation"), nil
	}
	if !user.HasPassword && !res.Trusted() {
		crosses, err := linksAnotherConnection(ctx, d, tx, res.UserID(), req.Identity)
		if err != nil {
			return false, nil, err
		}
		if crosses {
			return false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkCrossConnection, "identity from another connection needs a trusted link"), nil
		}
	}
	if err := d.LinkIdentityIn(ctx, tx, res.UserID(), req.Identity, res.Tenant()); err != nil {
		return false, nil, errors.Wrap(err, "Driver.LinkIdentityIn()")
	}
	req.UserID, req.Username = res.UserID(), user.Username
	if user.Disabled {
		return false, nil, nil
	}
	live, err = consumeInvitation(ctx, d, tx, req, false)

	return live, nil, err
}

// provisionUser writes the user the rule provisioned (signin.ProvisionUser), linked to
// the identity, and runs the rule's OnProvisioned. An identity whose verified address
// another user holds verified is refused instead (stop; see addressHeld). The
// invitation the sign-in carries is consumed before the row is written, so a dead one
// gives the new user nothing, not its address and not its grant, and a live one's
// address goes on the row (invitedAddress); a user provisioned disabled cannot accept
// and consumes nothing.
func provisionUser[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, res *signin.Resolution, byInvitation bool,
) (live bool, stop, err error) {
	newUser := res.NewUser()
	if newUser == nil {
		return false, nil, errors.New("the account rule provisioned no user: signin.Provision was given nil")
	}
	if stop, err = addressHeld(ctx, d, tx, req.Identity); err != nil || stop != nil {
		return false, stop, err
	}
	req.Username = newUser.Username
	insert := &dbtype.InsertSessionUser{Username: newUser.Username, PasswordHash: newUser.PasswordHash, Disabled: newUser.Disabled}
	if !newUser.Disabled {
		if live, err = consumeInvitation(ctx, d, tx, req, byInvitation); err != nil {
			return false, nil, err
		}
	}
	if live {
		if err := invitedAddress(ctx, d, tx, req, insert); err != nil {
			return false, nil, err
		}
	}
	id, err := d.InsertUserIn(ctx, tx, insert)
	if err != nil {
		return false, nil, errors.Wrap(err, "Driver.InsertUserIn()")
	}
	if err := d.LinkIdentityIn(ctx, tx, id, req.Identity, res.Tenant()); err != nil {
		return false, nil, errors.Wrap(err, "Driver.LinkIdentityIn()")
	}
	req.UserID = id
	if onProvisioned := res.OnProvisioned(); onProvisioned != nil {
		if err := onProvisioned(ctx, id); err != nil {
			return false, nil, errors.Wrap(err, "signin.Resolution.OnProvisioned()")
		}
	}

	return live, nil, nil
}

// addressHeld refuses the provisioning of a new user for an identity whose verified
// address another user already holds verified: that user is the person. The standard
// rules match the person by the address before they provision (preCreatedUser), so a
// rule that still provisions would make a second user for one person, and one
// without an address, since the address is held. The refusal is identity_rejected
// with signin.ErrEmailHeld as its cause, which names the holder for the log. An
// address the method did not verify, or one a user holds unverified, holds nothing
// against the provisioning.
func addressHeld[Tx any](ctx context.Context, d Driver[Tx], tx Tx, identity *sessioninfo.Identity) (stop, err error) {
	if !identity.EmailVerified || identity.Email == "" {
		return nil, nil
	}
	holder, err := d.UserByVerifiedEmailIn(ctx, tx, identity.Email)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByVerifiedEmailIn()")
	}
	if holder == nil {
		return nil, nil
	}
	cause := fmt.Errorf("%w: the verified address %s belongs to user %s (%s)", signin.ErrEmailHeld, identity.Email, holder.Username, holder.ID)

	return Refusal("", sessioninfo.RefusedIdentityRejected, cause, "identity rejected"), nil
}

// invitedAddress gives a user provisioned by a sign-in that carries a live invitation
// the invitation's address, verified, when no other user holds it: the person proved
// they read it, and the row is written with it since a row buffered in this
// transaction cannot be updated in it. On a store whose usernames are the addresses
// the address is the username, verified when it is the one the invitation went to.
func invitedAddress[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, insert *dbtype.InsertSessionUser) error {
	inv := req.Invitation
	if d.UsernameIsEmail() {
		insert.EmailVerified = strings.EqualFold(strings.TrimSpace(insert.Username), strings.TrimSpace(inv.Email))

		return nil
	}
	holder, err := d.UserByEmailIn(ctx, tx, inv.Email)
	if err != nil {
		return errors.Wrap(err, "Driver.UserByEmailIn()")
	}
	if holder == nil {
		email := inv.Email
		insert.Email, insert.EmailVerified = &email, true
	}

	return nil
}

// decideAndInsert is the second transaction of SignIn. It reads req's user, sets
// req.User and decides the sign-in. When the sign-in goes ahead it inserts the session.
// A refusal or an MFA wait is returned after the policy's own writes commit.
func decideAndInsert[Tx any](
	ctx context.Context, d Driver[Tx], id impulse.UUID, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest,
	source sessioninfo.UserSource, tenant string, policy signin.Policy[Tx], trusted bool,
) error {
	base := *req
	var v verdict
	err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts from the resolved request.
		*req, v = base, verdict{}

		user, err := d.UserIn(ctx, tx, req.UserID)
		if err != nil {
			return errors.Wrap(err, "Driver.UserIn()")
		}
		req.User = &sessioninfo.SignInUser{ID: req.UserID, Username: user.Username, HasPassword: user.HasPassword, Source: source, Tenant: tenant}

		var decide func(ctx context.Context) (*signin.Decision, error)
		if policy != nil {
			decide = func(ctx context.Context) (*signin.Decision, error) { return policy(ctx, tx, req) }
		}
		v, err = decideSignIn(ctx, req, user, decide, trusted)
		if err != nil {
			return err
		}
		if v.proceeds() {
			if err := d.InsertSessionIn(ctx, tx, id, insertSession, req); err != nil {
				return errors.Wrap(err, "Driver.InsertSessionIn()")
			}
		}

		// A refusal or an MFA wait commits the policy's writes without a session row.
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Driver.Transact()")
	}

	return v.err()
}

// verdict is decideSignIn's answer. The zero verdict lets the sign-in go ahead. A
// refusal or an MFA wait travels as a value rather than an error because the
// transaction that decided it commits; the flow answers it once it has.
type verdict struct{ stop error }

// proceeds reports whether the sign-in goes ahead.
func (v verdict) proceeds() bool {
	return v.stop == nil
}

// err returns the refusal (a *sessioninfo.LoginRefusal) or the MFA wait (a
// *signin.PendingSignInError), or nil when the sign-in goes ahead.
func (v verdict) err() error {
	return v.stop
}

// decideSignIn refuses a disabled user, sets req.Username to the user's, and asks
// decide when it is non-nil and the policy applies to req.Reason. The verdict is a
// refusal, an MFA wait or the go-ahead; the transaction commits on each. err is the
// policy's error, which rolls the transaction back.
//
// A RequireMFA answer is satisfied without a wait by a second factor the sign-in
// already carries: at an MFA completion, the step's events (completionSatisfies); at
// any sign-in, an attestation event, the sign-in's connection vouching for a second
// factor by evidence or by declaration (see sessioninfo.Attestation); and, where the
// answer admits one (no factor named, or the library's email code among them), a
// trusted device of the user the browser presents (trusted, checked before the
// transaction), which records a trusted-device event on the session in the step's
// place. Otherwise the sign-in waits for the factor.
func decideSignIn(
	ctx context.Context, req *sessioninfo.NewSessionRequest, user *dbtype.User, decide func(ctx context.Context) (*signin.Decision, error), trusted bool,
) (verdict, error) {
	if user.Disabled {
		return verdict{stop: Refusal("", sessioninfo.RefusedUserDisabled, signin.ErrUserDisabled, "User disabled")}, nil
	}
	req.Username = user.Username

	if decide == nil || !policyApplies(req.Reason) {
		return verdict{}, nil
	}

	decision, err := decide(ctx)
	if err != nil {
		return verdict{}, errors.Wrap(err, "signin.Policy()")
	}
	if decision == nil {
		decision = signin.Deny("")
	}

	switch decision.Outcome() {
	case signin.AllowSignIn:
		return verdict{}, nil
	case signin.HoldForMFA:
		if req.Reason == sessioninfo.ReasonStepUp && completionSatisfies(decision.Factors(), req.AuthEvents) {
			return verdict{}, nil
		}
		if attested(req.AuthEvents) {
			return verdict{}, nil
		}
		if trusted && admitsTrustedDevice(decision.Factors()) {
			req.AuthEvents = append(dbtype.InitialAuthEvents(req, nil), sessioninfo.AuthEvent{Method: sessioninfo.MethodTrustedDevice})

			return verdict{}, nil
		}
		pending := &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(req.UserID), Username: user.Username, Factors: decision.Factors()}
		if req.User != nil {
			pending.Tenant = req.User.Tenant
		}

		return verdict{stop: pending}, nil
	default:
		return verdict{stop: Refusal(decision.Refusal(), sessioninfo.RefusedByPolicy, denialCause(decision), "sign-in denied")}, nil
	}
}

// denialCause is the cause a Deny decision's refusal carries: ErrSignInDenied, and
// with it the cause one of the library's own rules named (Decision.Cause), so
// errors.Is matches either.
func denialCause(decision *signin.Decision) error {
	if cause := decision.Cause(); cause != nil {
		return fmt.Errorf("%w: %w", signin.ErrSignInDenied, cause)
	}

	return signin.ErrSignInDenied
}

// policyApplies reports whether the sign-in policy decides a session created for
// reason. It decides ReasonLogin, ReasonIdentityLinked, ReasonExternalAuth (a session
// the application starts for a user it authenticated itself) and ReasonStepUp (an MFA
// completion, decided again with the completion's events on the request, so a tenant
// disabled or switched to require sign-on through SSO during the wait is honored).
// Every other session was decided when the session it continues was established.
func policyApplies(reason sessioninfo.NewSessionReason) bool {
	switch reason {
	case sessioninfo.ReasonLogin, sessioninfo.ReasonIdentityLinked, sessioninfo.ReasonExternalAuth, sessioninfo.ReasonStepUp:
		return true
	default:
		return false
	}
}

// completionSatisfies reports whether an MFA completion's events satisfy a RequireMFA
// answer the policy gives at the completion: any completion when the answer names no
// factor, otherwise an event by one of the factors it names. A rule that still asks
// for the factor the person just gave proceeds; one that asks for another holds the
// sign-in again.
func completionSatisfies(factors []sessioninfo.AuthMethod, events []sessioninfo.AuthEvent) bool {
	if len(factors) == 0 {
		return true
	}
	for _, e := range events {
		if slices.Contains(factors, e.Method) {
			return true
		}
	}

	return false
}

// attested reports whether events carry an attestation: the sign-in's connection
// vouched for a second factor, by evidence or by declaration.
func attested(events []sessioninfo.AuthEvent) bool {
	return slices.ContainsFunc(events, func(e sessioninfo.AuthEvent) bool { return e.Method.Attests() })
}

// admitsTrustedDevice reports whether a RequireMFA answer naming factors is satisfied
// by a trusted device: when it names no factor, or the library's email code is among
// them, since a device is trusted after that code. An answer naming the application's
// own factors only is the application's step to run.
func admitsTrustedDevice(factors []sessioninfo.AuthMethod) bool {
	return len(factors) == 0 || slices.Contains(factors, sessioninfo.MethodEmailCode)
}

// Refusal returns a refused sign-in's error. It carries code (or fallback when code is
// empty) for the login page, cause for errors.Is, and an Unauthorized client message.
func Refusal(code, fallback sessioninfo.LoginRefusalCode, cause error, message string) error {
	if code == "" {
		code = fallback
	}

	return sessioninfo.NewLoginRefusal(code, httpio.NewUnauthorizedMessageWithError(cause, message))
}

// linksAnotherConnection reports whether identity would attach a new connection to
// userID: the user has links, and none of them is on identity's (Method, Connection).
// A user without a password cannot confirm a link, so an untrusted link of a connection
// the user does not sign in through yet is refused: the rule's evidence is the only
// check, and a custom rule must say so with trusted. A connection the user already signs in
// through, however many others they have, adds nothing and is not checked.
func linksAnotherConnection[Tx any](ctx context.Context, d Driver[Tx], tx Tx, userID impulse.UUID, identity *sessioninfo.Identity) (bool, error) {
	links, err := d.IdentitiesByUserIn(ctx, tx, userID)
	if err != nil {
		return false, errors.Wrap(err, "Driver.IdentitiesByUserIn()")
	}
	if len(links) == 0 {
		return false, nil
	}
	for _, link := range links {
		if link.Method == identity.Method && link.Connection == identity.Connection {
			return false, nil
		}
	}

	return true, nil
}
