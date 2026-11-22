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
	"github.com/impulseframework/impulse/session/internal/util"
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
	// matched without regard to the case of its ASCII letters as the users table's
	// normalized address is, or nil when there is none; an address a user holds
	// unverified matches no one.
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
	// VerifyUserEmailIfHeldIn writes email on the user as their address, confirmed at
	// the transaction's time, inside tx, only while the user's row still holds held byte
	// for byte (nil: no address) and no other user holds email, and reports whether it
	// wrote: the address a mailed sign-in code confirms, written with the session. When
	// it reports false nothing is written and no statement has failed, so tx goes on and
	// can commit. On a store whose usernames are the addresses it writes the confirmed
	// time alone, and only where the row holds held and email is the user's username;
	// held nil writes nothing there.
	VerifyUserEmailIfHeldIn(ctx context.Context, tx Tx, userID impulse.UUID, held *string, email string) (bool, error)
	// ClaimUserNameIn writes name on the user inside tx when the user holds none, and
	// reports whether it wrote. A user inserted earlier in tx is not claimed for: its
	// insert carries the name.
	ClaimUserNameIn(ctx context.Context, tx Tx, userID impulse.UUID, name string) (bool, error)
	// UserByEmailIn reads the user holding email inside tx, verified or not, matched
	// without regard to the case of its ASCII letters, or nil when there is none.
	UserByEmailIn(ctx context.Context, tx Tx, email string) (*dbtype.User, error)
	// UsernameIsEmail reports whether the store's usernames are the addresses.
	UsernameIsEmail() bool
	// SignInMethodIn reads the row of the sign-in method registered under name inside
	// tx: what an administrator chose for the method. It answers nil when the method
	// has no row, which is a method that is invite only.
	SignInMethodIn(ctx context.Context, tx Tx, name string) (*dbtype.SignInMethod, error)
}

// InvitationAcceptor is the application's half of an invitation's acceptance: it
// gives userID what the invitation names (its roles, in its tenant), inside the
// transaction that consumes the invitation, which it is handed: the invitation is
// spent and its roles given together, or neither. It reads and writes through tx
// alone: the Spanner emulator allows one read-write transaction at a time, so a second
// transaction or a read through another client inside the hook waits on the one it
// runs in. A user the same transaction provisioned is buffered and not readable in it
// (Spanner applies a buffered row at the commit), so the user is named by id, which is
// what the hook writes under, and by username, for what it reports.
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
// userID, named by id and by username, with seats, the default seat of the tenant
// whose directory the identity came through, inside the transaction that links the
// identity to the user, which it is handed: the link is made and the person seated
// together, or neither. The link is made at a person's first sign-in through the
// directory, whether the account is made in that transaction or stood before, so the
// seater is called once for a link; it gives a seat only where the person holds none in
// the tenant, which it alone can read. It is held to what InvitationAcceptor is: tx
// alone for reads and writes, a committed function (nil for none) the flow runs once
// the transaction has committed and before the sign-in policy decides, and an error
// that fails the transaction, so no account is left holding nothing and no link is
// left that gave nothing.
type DirectorySeater[Tx any] func(ctx context.Context, tx Tx, userID impulse.UUID, username string, seats []signin.DirectorySeat) (committed func(ctx context.Context), err error)

// OnlyInTenant is the application's half of the rule on a first link made on a name
// alone (linkUser): it reports whether tenant is the only place userID holds anything,
// read through tx, the transaction that would make the link. The user holds a role, or
// a place in a group, in tenant itself, and nothing in another tenant, in every tenant
// or outside the tenants: a tenant's directory vouches for the people who are the
// tenant's alone, since the link hands its directory everything the account holds. It
// is held to what InvitationAcceptor is for its reads: tx alone.
type OnlyInTenant[Tx any] func(ctx context.Context, tx Tx, userID impulse.UUID, tenant accesstypes.Domain) (bool, error)

// SignUpSeater is the application's half of a sign-up through a method an administrator
// opened: it gives userID, named by id and by username, the default role the method's
// row names, inside the transaction that makes the account, which it is handed: the
// account is made and holds the role together, or neither. It is called for a person
// the open method admitted, never for one whose account stood before, and at a
// directory's method not for one who came with a live invitation, whose roles are the
// invitation's. A person who signs up with a password from a mailed link is given the
// role, and an invitation the browser also carries is then accepted only where it was
// mailed to the address the sign-up verified. method is the name the method is
// registered under. It is held to what InvitationAcceptor is: tx alone for
// reads and writes, a committed function (nil for none) the flow runs once the
// transaction has committed and before the sign-in policy decides, and an error that
// fails the transaction, so no account is left holding nothing.
type SignUpSeater[Tx any] func(ctx context.Context, tx Tx, userID impulse.UUID, username, method, role string) (committed func(ctx context.Context), err error)

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
	// Account is the registration's own account rule, nil for none; see SignIn.
	Account signin.AccountRule[Tx]
	// Method is the name the sign-in's method is registered under, by which the
	// standard rule reads what an administrator chose for it (the method's row of the
	// sign-in methods table). A sign-in that names none is invite only.
	Method string
	// Audience is whom the method's registration admits now, as it spells itself
	// (signin.Audience.Key): the method's row opens it only where the row was opened for
	// that audience (dbtype.SignInMethod.OpenFor).
	Audience string
	// Policy is the sign-in policy, nil for none.
	Policy signin.Policy[Tx]
	// Accept is the application's invitation acceptor, nil for none.
	Accept InvitationAcceptor[Tx]
	// SignUp gives a person who signed up through an open method the method's default
	// role, nil for none; see standardRule.
	SignUp SignUpSeater[Tx]
	// Directories reads the declaration of the directory an identity came through at a
	// person's first sign-in through it, nil for none, and Seat seats the person with
	// the default seat the declaration names, nil for none; see standardRule. OnlyIn
	// reports whether a tenant is the only place a person holds anything, nil for
	// none: with Directories it decides a first link made on a name alone; see
	// linkUser.
	Directories signin.DirectoryReader[Tx]
	Seat        DirectorySeater[Tx]
	OnlyIn      OnlyInTenant[Tx]
	// SeededUsers says the registration declared its provider's names the
	// application's own (signin.Registration.SeededUsers): the standard rule's first
	// link of a user created ahead is then made on the name alone; see firstLink.
	SeededUsers bool
	// NoPassword says the Auth registers no password method. No sign-in can then prove
	// a password, so a password an account holds (one the application's API set, or
	// one kept from a release that had the method) is no reason to hold a sign-in for
	// a confirmation no route can take: the standard rule and the link decide such an
	// account as they decide one with no password; see asRegistered. A rule of the
	// registration's own that asks for the confirmation all the same is refused; see
	// applyResolution.
	NoPassword bool
	// TrustedDevice reports whether the browser is a trusted device of the user, nil
	// for none; see decideSignIn.
	TrustedDevice TrustedDeviceCheck
	// Proven is the proof of the password the sign-in rests on, nil when no password
	// was proved: the session is inserted only while the user still holds that password
	// (decideAndInsert).
	Proven dbtype.PasswordProof
	// ConfirmAddress says a sign-in through a directory that does not vouch for the
	// person's address is held for the code that confirms one, while the account holds
	// no confirmed address (addressHold), and that a sign-in to an account that cannot
	// take the address the method vouches for, because another account holds it, is
	// refused (addressTaken). The standard rule's first link to an account created
	// ahead, on a sign-in name the method does not vouch for, waits for the code
	// whatever this says (nameAloneHold). The Auth sets it for every registration but
	// the deployment's own provider of seeded accounts.
	ConfirmAddress bool
	// WaitedOn is the user a pending sign-in waited on, for the sign-in that completes
	// the wait; it is not valid for any other. The rules' answer for an identity that is
	// still linked to nobody is held to that user before anything is written
	// (waitedOnAnother).
	WaitedOn impulse.NullUUID
	// CodeAddress is the address the account's own mailed sign-in code confirms, for the
	// sign-in that completes a wait with that code, and nil for every other. It is
	// written on the account, confirmed, in the transaction that inserts the session and
	// only then (decideAndInsert); a first link that rests on the code is made only while
	// the account still holds it (nameAloneHold); and it is what lets an external
	// sign-in through to an account with no confirmed address (addressHold).
	CodeAddress *dbtype.CodeAddress
}

// SignIn establishes session id for a request that carries a verified identity, in two
// transactions (see the README's "Storage and schema"). The first resolves an external
// identity's user: a linked identity is its user's, and an unlinked one goes to the
// account rules: the registration's custom rule when it has one, and the standard rule
// when it has none or the custom rule answers signin.Standard. The standard rule reads
// what an administrator chose for the method, in that transaction (standardRule). The second reads the
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
// (invitation_address), the invitation left standing for the invited account. The
// link only opens the invitation: whoever holds it may have been forwarded it. So a
// password account, or one the application signs in itself, accepts at once only where
// it holds the invited address confirmed; one that holds no address, or holds the
// invited one and has not confirmed it, waits for the code the library mails to the
// invited address, the wait naming the account and nothing written (acceptedByCode,
// namedCodeWait). An external identity that asserts no
// address, or one its method did not verify, has none to compare: its acceptance rests
// on the code the library mails to the invited address (rules.ConfirmAddress), so the
// sign-in waits, naming no user and writing nothing, until it carries that proof
// (Invitation.AddressProven; see acceptsByCode, resolveIn). A registration of accounts
// the deployment seeds is mailed no code: the address its identity asserts is compared,
// and a sign-in that asserts none is refused as one for another address is. The invitation is told how the
// person signed in (Invitation.SignInMethod, SignInConnection: the identity's), which
// the acceptor reads, and an invitation that makes a tenant entered through its
// directory only
// (Invitation.NewTenant) is accepted through an organization's directory alone: a
// sign-in by password or with a personal account is refused before anything is read or
// written (organization_account_required), the invitation left standing
// (organizationAccountRefusal). Accepting it consumes the invitation and sets the
// user's address from it when the user holds no verified address, in the resolution
// transaction for an external identity (so a user the invitation provisioned and the
// consumption commit together) and in a transaction of its own for a password
// identity; rules.Accept gives what the invitation names inside that same transaction,
// which it is handed, so the invitation is spent and its roles given together or
// neither is. The invitation is consumed only once the resolution has admitted the
// sign-in: the user found, linked or provisioned, and enabled. A sign-in the account
// rules refuse or hold for a confirmation, a disabled user's, or a password sign-in
// whose password changed while it waited (signInHold), leaves the invitation standing,
// nothing granted, so the person may sign in another way; a sign-in the policy then
// denies or holds leaves it accepted, and what the acceptance granted stands, the
// invited address with it. An
// invitation that is no longer live is skipped and the sign-in goes on, unless the
// invitation alone admitted the person (a method that is invite only), which is then a
// refusal (invitation_expired) and provisions nothing; req.Invitation.Accepted reports
// whether it was accepted. An acceptor that fails ends the sign-in with its error and
// fails the transaction: the invitation stands, a user the resolution would have
// provisioned or linked is not, and Accepted is false.
//
// A person's first sign-in through a directory some tenant declares is decided by the
// tenant's row when rules.Directories is set (standardRule): the default seat is given
// by rules.Seat inside the resolution's transaction, the one that links the identity,
// before the invitation's grant, whether the account is made in that transaction or
// stood before, so the link is made and the person seated together or neither is. A
// linked identity gives nothing more: a seat an administrator removed is not given
// back by signing in, and a link that is deleted and made again is a first sign-in
// again.
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

	if stop := invitedAddressRefusal(req, rules.ConfirmAddress); stop != nil {
		return stop
	}
	if stop := signedInThrough(req); stop != nil {
		return stop
	}
	source, tenant := sessioninfo.UserNamed, ""
	switch {
	case dbtype.IsExternal(req.Identity):
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
			// The address was held to the invited one above, and the acceptance is held
			// to the code mailed to it unless the account holds it confirmed: the sign-in
			// waits for the code, naming its account, with nothing written. A dead
			// invitation is skipped and the sign-in goes on, unless the sign-in entered
			// the code mailed for it: that code is the invitation's acceptance and no
			// account's second factor, so the sign-in ends with the invitation, nobody
			// signed in and nothing written. An acceptor that fails ends it, the
			// invitation standing. The acceptance is apart from the session: it is made
			// before the sign-in policy decides, so that the policy sees what the
			// invitation gave, and the invited address is written with it, so a sign-in
			// the policy then denies or holds leaves the invitation accepted and that
			// address on the account. One whose password changed while it waited, or
			// whose user was disabled, accepts nothing (signInHold): the first ends with
			// dbtype.ErrPasswordChanged, the second is refused user_disabled below.
			hold := &signInHold{proven: rules.Proven, claim: rules.CodeAddress}
			dead, stop, err := acceptInvitation(ctx, d, resolved.UserID, resolved.Username, req.Invitation, rules.Accept, namedCodeWait, hold)
			if err != nil {
				return err
			}
			if stop != nil {
				return stop
			}
			if dead && req.Invitation.AddressProven {
				return Refusal("", sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired, InvitationEnded)
			}
		}
	}

	trusted, err := trustedDevice(ctx, resolved.UserID, rules.TrustedDevice)
	if err != nil {
		return err
	}

	return decideAndInsert(ctx, d, id, insertSession, &resolved, source, tenant, rules, trusted)
}

// invitedAddressRefusal refuses a sign-in that carries an invitation mailed to another
// address: the address the sign-in asserts (Identity.Email: the directory's for an
// external identity, the account's own for a password one) must be the invited one, so
// an invitation is accepted only by the account it was sent to, whoever holds the link
// (addressRefusal). Nothing is read or written: the invitation stands and the login
// page, which still carries it, names the invited address. Nil when the sign-in
// carries no invitation or the acceptance may go on.
//
// A sign-in through a directory that asserts no address, or asserts one its method did
// not verify, has none to compare: whoever runs a directory writes the address it sends,
// so the unverified one is nobody's proof, whether it is the invited address or another.
// Such an acceptance is not refused here (confirm): the resolution holds it to the code
// mailed to the invited address (acceptsByCode), which is the proof that the person
// reads that mailbox. For a registration whose accounts the deployment seeds, no code is
// mailed and the address its own provider asserts is compared.
func invitedAddressRefusal(req *sessioninfo.NewSessionRequest, confirm bool) error {
	if req.Invitation == nil || acceptsByCode(req, confirm) {
		return nil
	}

	return addressRefusal(req.Identity.Email, !dbtype.IsExternal(req.Identity), req.Invitation)
}

// acceptsByCode reports whether the sign-in carries an invitation its identity cannot
// answer for by an address of its own, through a registration whose addresses are
// confirmed by the mailed code (confirm): an external identity that asserts none (Microsoft Entra ID sends none unless the registration is
// set up to), or asserts one its method did not verify (Identity.EmailVerified: the
// address Microsoft Entra ID sends is whatever the directory's administrator wrote, and
// anybody can run a directory). Such an acceptance rests on the code the library mails
// to the invited address, and the address the identity asserts is not read.
func acceptsByCode(req *sessioninfo.NewSessionRequest, confirm bool) bool {
	if !confirm || req.Invitation == nil || !dbtype.IsExternal(req.Identity) {
		return false
	}

	return strings.TrimSpace(req.Identity.Email) == "" || !req.Identity.EmailVerified
}

// invitedCodeWait is the wait of an acceptance that rests on the code mailed to the
// invited address and has not entered it yet: the sign-in is held for the email code,
// which the library mails to the invited address and nowhere else. It names no user:
// none is made, linked or changed before the code is entered, and the invitation is
// accepted in the transaction of the sign-in that carries the code.
func invitedCodeWait() error {
	return &signin.PendingSignInError{Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUID{}, Username: "", Tenant: "", Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode}}
}

// provenAddress is the invited address a sign-in proved by the code mailed to it, "" for
// none: the address of the invitation of an acceptance that rests on the code
// (acceptsByCode), once the sign-in has entered it (Invitation.AddressProven).
func provenAddress(req *sessioninfo.NewSessionRequest, confirm bool) string {
	if !acceptsByCode(req, confirm) || !req.Invitation.AddressProven {
		return ""
	}

	return req.Invitation.Email
}

// knownHolder finds the user who holds the address a sign-in has shown to be the
// person's, and answers that address with them: nil where nobody holds it, and "" where
// the sign-in has shown none. The address the method verified for the identity
// (Identity.EmailVerified: Google alone does, and only for a Gmail address or a
// Workspace account's; no other method's word counts) counts for the user who holds
// it verified, and one a user holds unverified matches no one. Or else the address is
// proven, the invited address whose mailed code the sign-in entered, and it counts for
// the user who holds it, confirmed or not: an address an administrator entered is kept
// for its account, the invitation mailed to it is that account's own (ownInvitation),
// and the code is what confirms it.
//
// It is what finds the account a person already has (preCreatedUser) and what keeps a
// second account from being made for them (addressHeld). It is not what links a second
// identity to an account somebody already signs in to: that takes the directory's own
// word (holdsAddress), so the code adds no way in to such an account.
func knownHolder[Tx any](ctx context.Context, d Driver[Tx], tx Tx, identity *sessioninfo.Identity, proven string) (holder *dbtype.User, address string, err error) {
	switch {
	case identity.EmailVerified && identity.Email != "":
		holder, err = d.UserByVerifiedEmailIn(ctx, tx, identity.Email)
		if err != nil {
			return nil, "", errors.Wrap(err, "Driver.UserByVerifiedEmailIn()")
		}

		return holder, identity.Email, nil
	case proven != "":
		holder, err = d.UserByEmailIn(ctx, tx, proven)
		if err != nil {
			return nil, "", errors.Wrap(err, "Driver.UserByEmailIn()")
		}

		return holder, proven, nil
	default:
		return nil, "", nil
	}
}

// notTheInvitedName refuses the acceptance that rests on the mailed code (proven, "" for
// a sign-in that rests on none) by an account under another name, in a store whose
// usernames are the addresses. There an account's address is its name: the invited
// address is not this account's, could be written nowhere on it, and the code that
// proved it would stand for an address the account does not hold. The refusal is the
// one of an acceptance with another address. Nil in a store that keeps the address
// apart, where the account takes the invited address with the acceptance.
func notTheInvitedName[Tx any](d Driver[Tx], inv *sessioninfo.Invitation, proven, username string) error {
	if proven == "" || !d.UsernameIsEmail() || sameAddress(proven, username) {
		return nil
	}

	return Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, InvitedAddressMessage(inv))
}

// notTheInvited refuses the acceptance that rests on the mailed code (proven, "" for a
// sign-in that rests on none) by user, an account the rule found for an identity it is
// not linked to yet, where user is not the account the invitation was mailed to: another
// account holds the invited address, confirmed or not, or user is under another name
// in a store whose usernames are the addresses (notTheInvitedName). An account found
// under the directory's name would otherwise spend an invitation that is another
// account's own, on a code that proves an address it cannot take. The refusal is the
// one of an acceptance with another address, before anything is written.
func notTheInvited[Tx any](ctx context.Context, d Driver[Tx], tx Tx, inv *sessioninfo.Invitation, proven string, user *dbtype.User) (stop, err error) {
	if proven == "" {
		return nil, nil
	}
	if stop := notTheInvitedName(d, inv, proven, user.Username); stop != nil {
		return stop, nil
	}
	holder, err := d.UserByEmailIn(ctx, tx, proven)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByEmailIn()")
	}
	if holder != nil && holder.ID != user.ID {
		return Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, InvitedAddressMessage(inv)), nil
	}

	return nil, nil
}

// namedCodeWait is the wait of an acceptance by an account that signed in by naming
// itself, with its password or in a session the application starts, and has not shown
// the invited address to be its own: the sign-in is held for the email code, which the
// library mails to the invited address. The person is known, so the wait names the
// account; nothing is written before the code is entered, and the invitation is
// accepted in the transaction of the sign-in that carries the code. That transaction is
// the one place this code writes the invited address on an account: the code is the
// invitation's and no second factor of the account, so a
// sign-in that entered it and finds the invitation dead ends there, with nobody signed
// in and no address taken.
func namedCodeWait(user *dbtype.User, _ *sessioninfo.Invitation) error {
	return &signin.PendingSignInError{
		Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(user.ID), Username: user.Username, Tenant: "", Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
	}
}

// signedInCodeRefusal is the answer to an acceptance by hand, by a person signed in
// whose account has not shown the invited address to be its own: a session cannot wait
// for a code, so the acceptance is refused (invitation_address, with
// signin.ErrInvitationCode among its causes) and the sentence says what to do
// (InvitedCodeMessage). Nothing is written and the invitation stands.
func signedInCodeRefusal(_ *dbtype.User, inv *sessioninfo.Invitation) error {
	cause := fmt.Errorf("%w: %w", signin.ErrInvitationAddress, signin.ErrInvitationCode)

	return Refusal("", sessioninfo.RefusedInvitationAddress, cause, InvitedCodeMessage(inv))
}

// InvitedCodeMessage is the sentence shown for an acceptance by hand that was refused
// because the account has not confirmed the invited address: what the person does
// instead, and where the code goes.
func InvitedCodeMessage(inv *sessioninfo.Invitation) string {
	return fmt.Sprintf("To accept this invitation, sign in again from the invitation's link. A code will be sent to %s to confirm the address is yours.", inv.Email)
}

// acceptedByCode decides the acceptance of inv by user, an account whose sign-in does
// not itself answer for the invited address: one an identity is linked to that asserts
// no address, or one its method did not verify (acceptsByCode), one that signed in by
// naming itself (a password, a session the application starts), and one whose person,
// signed in, accepts by hand. The link proves nothing, since anybody may have been
// forwarded it, so the account is held to the address. The account
// that holds the invited address confirmed is the one invited, and accepts. An account
// that holds another confirmed address is not, and neither is any account while
// another one holds the invited address, confirmed or not (an address an administrator
// entered is kept for its account): both are refused as an acceptance with another
// address is. So is an account under another name in a store whose usernames are the
// addresses, which could not take the invited one. An account that has confirmed no
// address accepts once the sign-in has entered the code mailed to the invited address
// (Invitation.AddressProven), and until then is answered wait: the wait for the code
// where a sign-in can be held for it, the refusal that says to sign in again where it
// cannot. An account that holds the invited address unconfirmed is mailed its own code
// at that address, which proves the mailbox as the invitation's code does: it accepts
// once the sign-in carries that code's address (claim, the rules' CodeAddress), while it
// still holds the address. The acceptance writes the address on the account, confirmed:
// the invited one, or, where it rests on the account's own code (byOwnCode), the code's
// address in the account's own spelling, and only while the account still holds it as
// it held it when the code was checked (acceptInvitation).
func acceptedByCode[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, inv *sessioninfo.Invitation, user *dbtype.User, wait error, claim *dbtype.CodeAddress,
) (stop error, byOwnCode bool, err error) {
	holder, err := d.UserByEmailIn(ctx, tx, inv.Email)
	if err != nil {
		return nil, false, errors.Wrap(err, "Driver.UserByEmailIn()")
	}
	holds := holder != nil && holder.ID == user.ID
	ownCode := holds && claim != nil && sameAddress(claim.Email, inv.Email)
	switch {
	case holds && user.EmailConfirmed:
		return nil, false, nil
	case holder != nil && !holds, user.EmailConfirmed, d.UsernameIsEmail() && !sameAddress(inv.Email, user.Username):
		return Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, InvitedAddressMessage(inv)), false, nil
	case !inv.AddressProven && !ownCode:
		return wait, false, nil
	default:
		return nil, ownCode, nil
	}
}

// signedInThrough notes on the invitation the sign-in carries how the person signed in,
// the method and the connection of the sign-in's identity, for the acceptance hook to
// read, and holds the sign-in to what the invitation asks of it
// (organizationAccountRefusal). Nothing is read or written. Nil when the sign-in
// carries no invitation or the acceptance may go on.
func signedInThrough(req *sessioninfo.NewSessionRequest) error {
	inv := req.Invitation
	if inv == nil {
		return nil
	}
	inv.SignInMethod, inv.SignInConnection = req.Identity.Method, req.Identity.Connection

	return organizationAccountRefusal(inv)
}

// organizationAccountRefusal is the refusal (organization_account_required) of an
// acceptance of an invitation that makes a tenant entered through its directory only
// (NewTenant.DirectoryOnly) by a person who did not come through an organization's
// directory: a password, a personal account (a Google account in no Workspace, a
// Microsoft personal account) or a session the application started itself, read off
// the invitation's SignInMethod and SignInConnection (signin.Directory.Organization).
// The tenant's directory is the one its first administrator came through, and such a
// tenant could be entered by nobody without one, so the acceptance waits for a sign-in
// through the organization's directory. Nothing is read or written: no account and no
// tenant is made, and the invitation stands. Nil when the acceptance may go on.
func organizationAccountRefusal(inv *sessioninfo.Invitation) error {
	if inv.NewTenant == nil || !inv.NewTenant.DirectoryOnly {
		return nil
	}
	if (signin.Directory{Method: inv.SignInMethod, Connection: inv.SignInConnection}).Organization() {
		return nil
	}

	return Refusal("", sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired, OrganizationAccountMessage)
}

// OrganizationAccountMessage is the sentence shown for an acceptance refused because
// the invitation is accepted through an organization's directory alone: what the
// person does instead.
const OrganizationAccountMessage = "This invitation is accepted with your organization's account, not with a password or a personal account. " +
	"Sign in with your organization's account (Google Workspace or Microsoft Entra)."

// OrganizationHasTenantMessage is the sentence shown to a signed-in person whose
// acceptance by hand was refused because the organization they signed in through
// already has a tenant: it says what to do and nothing of that tenant.
const OrganizationHasTenantMessage = "Your organization is already set up in this application. Ask its administrator to invite you."

// addressRefusal is the refusal (invitation_address) of an acceptance of inv by an
// account whose address, email, is not the invited one, compared without regard to
// case and surrounding space. An account with no address ("") is not refused here when
// mayHoldNone says so: a password account or a person signed in, who can take the
// invited address by accepting and is held to the code mailed to it instead
// (acceptedByCode); a directory identity that asserts no address cannot match. Nil
// when the acceptance may go on to that check.
func addressRefusal(email string, mayHoldNone bool, inv *sessioninfo.Invitation) error {
	email = strings.TrimSpace(email)
	if email == "" && mayHoldNone {
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

// sameAddress reports whether two addresses are one, without regard to the case of
// their ASCII letters and surrounding space; an empty address is nobody's and matches
// nothing. A letter from outside ASCII equals only itself: Unicode's case folding takes
// the Kelvin sign (U+212A) for k, and an address written with one is another mailbox.
func sameAddress(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)

	return a != "" && util.EqualASCIIFold(a, b)
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

// InvitationEnded is what a person is told when the invitation their sign-in rests on
// no longer stands, in every place that refuses it.
const InvitationEnded = "That invitation has expired or was already used. Ask the person who invited you to send a new one"

// errInvitationDead ends the resolution transaction of a sign-in that its invitation
// alone admitted, or that rests on the code mailed for the invitation, once the
// invitation turns out to be dead: nothing is committed.
var errInvitationDead = errors.New("the invitation has expired or was already used")

// consumeInvitation consumes the invitation req carries, if any, inside the
// resolution's transaction once it has admitted the sign-in (for a provisioned user,
// before the row is written), and reports whether it was live. A dead invitation is
// reported dead and the sign-in goes on without it, unless the sign-in stands on it
// (onInvitation), which ends the transaction with errInvitationDead: it alone admitted
// the person (the rule's byInvitation), or the sign-in entered the code mailed to the
// invited address (provenAddress), which was mailed for the invitation's acceptance and
// is the proof of no other sign-in, of no other address and of no second factor.
func consumeInvitation[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, onInvitation bool) (live bool, err error) {
	inv := req.Invitation
	if inv == nil {
		return false, nil
	}
	inv.Accepted = false
	if err := d.ConsumeMailLinkIn(ctx, tx, inv.ID, time.Now()); err != nil {
		if !httpio.HasNotFound(err) {
			return false, errors.Wrap(err, "Driver.ConsumeMailLinkIn()")
		}
		if onInvitation {
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
		if err := claimInvitedName(ctx, d, tx, req.UserID, req.Invitation); err != nil {
			return err
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
// fails the transaction: the invitation stands. An invitation that makes a tenant is
// made by the acceptor and nothing else, so with no acceptor it is an error, and the
// invitation stands: consuming it would spend the link and make nothing.
func grantConsumed[Tx any](
	ctx context.Context, tx Tx, userID impulse.UUID, username string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx],
) (committed func(ctx context.Context), err error) {
	if accept == nil {
		if inv != nil && inv.NewTenant != nil {
			return nil, errors.Newf("the invitation %s makes a tenant, and the Auth has no acceptance hook to make it with (SetInvitationAccepted): nothing is accepted", inv.ID)
		}

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
	if inv == nil || inv.TenantID == "" || len(seats) == 0 {
		return seats
	}

	return slices.DeleteFunc(slices.Clone(seats), func(seat signin.DirectorySeat) bool {
		return string(seat.Tenant) == inv.TenantID && slices.Contains(inv.RoleNames, string(seat.Role))
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
// nothing written, the invitation left standing (addressRefusal). The link proves
// nothing of the address, so the user accepts by hand only where their account holds
// the invited address confirmed, read in the transaction: a user with no address, or
// one who has not confirmed the invited one, is refused the same way with
// signin.ErrInvitationCode among the causes (signedInCodeRefusal), and accepts by
// signing in again from the invitation, which waits for the code mailed to the invited
// address. An invitation that is no longer
// live (consumed, expired or revoked) is NotFound with signin.ErrInvitationExpired as
// its cause, and nothing is written; a grant that fails is the acceptor's error,
// whatever it is, with nothing written either: the invitation stands.
//
// The caller notes on inv how the person's session was established
// (inv.SignInMethod, inv.SignInConnection: the session's first auth event), which
// accept reads, and the acceptance is held to it: an invitation that makes a tenant
// entered through its directory only is refused (organization_account_required) for a
// session that came through no organization's directory, with nothing written
// (organizationAccountRefusal).
func AcceptInvitation[Tx any](ctx context.Context, d Driver[Tx], userID impulse.UUID, username, email string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx]) error {
	inv.Accepted = false
	if stop := addressRefusal(email, true, inv); stop != nil {
		return stop
	}
	if stop := organizationAccountRefusal(inv); stop != nil {
		return stop
	}
	dead, stop, err := acceptInvitation(ctx, d, userID, username, inv, accept, signedInCodeRefusal, nil)
	if err != nil {
		return err
	}
	if dead {
		return httpio.NewNotFoundMessageWithError(signin.ErrInvitationExpired, InvitationEnded)
	}

	return stop
}

// errAcceptanceStopped ends the transaction of an acceptance outside a resolution once
// the account turns out not to have shown the invited address to be its own: the
// invitation's consumption is rolled back and nothing is committed.
var errAcceptanceStopped = errors.New("the acceptance waits for the code mailed to the invited address")

// errAcceptanceSkipped ends the transaction of a sign-in's acceptance whose user is
// disabled: nothing is committed, and the sign-in goes on to its refusal.
var errAcceptanceSkipped = errors.New("the user is disabled: the sign-in accepts nothing")

// signInHold is what the acceptance a sign-in makes outside a resolution is held to:
// the password the sign-in proved (proven, the rules' Proven; the nil proof holds it to
// nothing), and the address the account's own mailed code confirms (claim, the rules'
// CodeAddress), nil where the sign-in carries none.
type signInHold struct {
	proven dbtype.PasswordProof
	claim  *dbtype.CodeAddress
}

// acceptInvitation is the transaction of an acceptance outside a resolution: a password
// sign-in's, and a signed-in person's (AcceptInvitation). dead reports an invitation
// that is no longer live, with nothing written and no error. stop is the answer to an
// account that has not shown the invited address to be its own (acceptedByCode), decided
// in the transaction on the user row it reads, with nothing written and no error: a
// refusal, or what unproven answers for an account that may still enter the code mailed
// to the invited address. An error is the driver's or the acceptor's, and nothing is
// written either. inv.Accepted is set to the outcome.
//
// signIn is what the acceptance a sign-in makes is held to, nil for one by hand. It goes
// on only while the user, read in its transaction, still holds the password the sign-in
// proved, and is enabled: a password changed since the proof ends it with
// dbtype.ErrPasswordChanged, and a disabled user's acceptance is skipped with no error,
// the sign-in then refused user_disabled; nothing is written either way, and the
// invitation stands. Where the acceptance rests on the account's own mailed code
// (acceptedByCode's byOwnCode), the code's address is written in the account's own
// spelling, and only while the account still holds what it held when the code was
// checked and no other account holds the address (Driver.VerifyUserEmailIfHeldIn);
// otherwise the code no longer stands, stop is the code_expired refusal
// (codeAddressGone), and nothing is written: the invitation stands. The acceptance is
// made before the sign-in policy decides, so that the policy sees what it gave: a
// sign-in the policy then denies or holds leaves the invitation accepted and the
// address written.
func acceptInvitation[Tx any](
	ctx context.Context, d Driver[Tx], userID impulse.UUID, username string, inv *sessioninfo.Invitation, accept InvitationAcceptor[Tx],
	unproven func(user *dbtype.User, inv *sessioninfo.Invitation) error, signIn *signInHold,
) (dead bool, stop, err error) {
	var claim *dbtype.CodeAddress
	if signIn != nil {
		claim = signIn.claim
	}
	inv.Accepted = false
	var committed committedWork
	err = d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts with nothing owed and the invitation taken for live.
		committed, dead, stop = nil, false, nil
		if err := d.ConsumeMailLinkIn(ctx, tx, inv.ID, time.Now()); err != nil {
			dead = httpio.HasNotFound(err)

			return errors.Wrap(err, "Driver.ConsumeMailLinkIn()")
		}
		user, err := d.UserIn(ctx, tx, userID)
		if err != nil {
			return errors.Wrap(err, "Driver.UserIn()")
		}
		if signIn != nil {
			if err := signIn.proven.Check(user.PasswordProof); err != nil {
				return errors.Wrap(err, "dbtype.PasswordProof.Check()")
			}
			if user.Disabled {
				return errAcceptanceSkipped
			}
		}
		var byOwnCode bool
		if stop, byOwnCode, err = acceptedByCode(ctx, d, tx, inv, user, unproven(user, inv), claim); err != nil {
			return err
		}
		if stop == nil && byOwnCode {
			wrote, err := d.VerifyUserEmailIfHeldIn(ctx, tx, userID, claim.Held, claim.Email)
			if err != nil {
				return errors.Wrap(err, "Driver.VerifyUserEmailIfHeldIn()")
			}
			if !wrote {
				stop = codeAddressGone(userID, claim)
			}
		}
		if stop != nil {
			// Nothing of the acceptance is kept: the invitation stands.
			return errAcceptanceStopped
		}
		if !byOwnCode {
			if _, err := d.ClaimVerifiedEmailIn(ctx, tx, userID, inv.Email); err != nil {
				return errors.Wrap(err, "Driver.ClaimVerifiedEmailIn()")
			}
		}
		if err := claimInvitedName(ctx, d, tx, userID, inv); err != nil {
			return err
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
			return true, nil, nil
		}
		if stop != nil && errors.Is(err, errAcceptanceStopped) {
			return false, stop, nil
		}
		if errors.Is(err, errAcceptanceSkipped) {
			return false, nil, nil
		}

		return false, nil, errors.Wrap(err, "Driver.Transact()")
	}
	inv.Accepted = true
	committed.run(ctx)

	return false, nil, nil
}

// SignUpClosed is the answer to a sign-up through a method that is not open to it: the
// sentence the sign-up routes say when the method is invite only.
func SignUpClosed() error {
	return httpio.NewNotFoundMessageWithError(signin.ErrNotInvited, "sign-up is not offered here")
}

// SignUp makes the account of a person who signs up with a password, in one
// transaction, and returns its id. method is the name the password method is registered
// under and audience whom its registration admits (signin.Audience.Key), and the
// transaction reads what an administrator chose for it: a method that is not open to
// sign-up for that audience refuses (SignUpClosed), so a sign-up link mailed while the
// method was open creates nothing once it is closed. The mail link linkID, which
// carried the person here, is consumed, the user row written, and the method's default
// role given by seat, handed the transaction (nil for none; a role with no hook fails
// the transaction, as seatSignedUp says): the link is spent, the account made and the
// role held together, or none of it. What seat asks to have run after the commit runs
// once it has landed, before the caller signs the person in.
//
// A link that is no longer live is NotFound. An address another user holds, verified or
// not, and a username that is taken are a Conflict. Nothing is written by a refusal.
func SignUp[Tx any](
	ctx context.Context, d Driver[Tx], method, audience string, user *dbtype.InsertSessionUser, linkID impulse.UUID, seat SignUpSeater[Tx],
) (impulse.UUID, error) {
	if err := dbtype.ValidateInsertUser(user, d.UsernameIsEmail()); err != nil {
		return impulse.NilUUID, errors.Wrap(err, "dbtype.ValidateInsertUser()")
	}
	var (
		id        impulse.UUID
		committed committedWork
	)
	err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts with nothing owed.
		committed = nil
		chosen, err := d.SignInMethodIn(ctx, tx, method)
		if err != nil {
			return errors.Wrap(err, "Driver.SignInMethodIn()")
		}
		if !chosen.OpenFor(audience) {
			return SignUpClosed()
		}
		if user.Email != nil {
			holder, err := d.UserByEmailIn(ctx, tx, *user.Email)
			if err != nil {
				return errors.Wrap(err, "Driver.UserByEmailIn()")
			}
			if holder != nil {
				return httpio.NewConflictMessagef("the email address %q is already in use", *user.Email)
			}
		}
		if err := d.ConsumeMailLinkIn(ctx, tx, linkID, time.Now()); err != nil {
			return errors.Wrap(err, "Driver.ConsumeMailLinkIn()")
		}
		if id, err = d.InsertUserIn(ctx, tx, user); err != nil {
			return errors.Wrap(err, "Driver.InsertUserIn()")
		}
		seated, err := seatSignedUp(ctx, tx, id, user.Username, method, chosen.Role(), seat)
		if err != nil {
			return err
		}
		committed.add(seated)

		return nil
	})
	if err != nil {
		if d.SignInConflict(err) == dbtype.UsernameConflict {
			return impulse.NilUUID, httpio.NewConflictMessagef("username %q already exists", user.Username)
		}

		return impulse.NilUUID, errors.Wrap(err, "Driver.Transact()")
	}
	committed.run(ctx)

	return id, nil
}

// LinkConfirmed links identity to userID once the person has confirmed the link with
// the user's password, in one transaction, and gives the person the default seat of
// the tenant whose directory the identity came through, when directories reads one
// (nil for none): the confirmed link is the person's first sign-in through the
// directory, as a link the resolution makes is, so the seat is given with it or the
// link is not made (seatDeclared). What seat asks to have run after the commit runs
// once it has landed, before the caller establishes the session. An identity that was
// linked meanwhile is a Conflict with nothing written, which the caller reads the link
// by; a seater's error is the seater's, whatever it is, with nothing written either.
//
// proven is the proof of the password the person confirmed with: the link is made only
// while the user still holds that password, read in the transaction that makes it, so
// a password changed, reset or removed since the confirmation links nothing
// (dbtype.ErrPasswordChanged). The nil proof holds the link to nothing.
func LinkConfirmed[Tx any](
	ctx context.Context, d Driver[Tx], userID impulse.UUID, username string, identity *sessioninfo.Identity, tenant string,
	directories signin.DirectoryReader[Tx], seat DirectorySeater[Tx], proven dbtype.PasswordProof,
) error {
	var committed committedWork
	err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts with nothing owed.
		committed = nil
		if proven != nil {
			user, err := d.UserIn(ctx, tx, userID)
			if err != nil {
				return errors.Wrap(err, "Driver.UserIn()")
			}
			if err := proven.Check(user.PasswordProof); err != nil {
				return errors.Wrap(err, "dbtype.PasswordProof.Check()")
			}
		}
		if err := d.LinkIdentityIn(ctx, tx, userID, identity, tenant); err != nil {
			return errors.Wrap(err, "Driver.LinkIdentityIn()")
		}
		declared, err := declaredDirectory(ctx, tx, identity, directories)
		if err != nil {
			return err
		}
		seated, err := seatDeclared(ctx, tx, userID, username, defaultSeats(declared), seat)
		if err != nil {
			return err
		}
		committed.add(seated)

		return nil
	})
	if err != nil {
		if d.SignInConflict(err) == dbtype.IdentityConflict {
			return httpio.NewConflictMessageWithError(err, "the identity is already linked")
		}

		return errors.Wrap(err, "Driver.Transact()")
	}
	committed.run(ctx)

	return nil
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
			return "", "", nil, Refusal("", sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired, InvitationEnded), nil
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
// handed tx: the default seats of a user the resolution provisioned or linked
// (rules.Seat), then
// what the consumed invitation names (rules.Accept), each written with the row that
// causes it or not at all, and a role both would give given once, by the invitation
// (seatsBeyondInvitation). A person an open method admitted is given the method's
// default role (rules.SignUp) unless they came with a live invitation, whose roles are
// what they get. committed is what the hooks ask to have run once tx has
// committed.
func resolveIn[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (source sessioninfo.UserSource, tenant string, committed committedWork, stop, err error) {
	link, err := d.IdentityIn(ctx, tx, req.Identity.Method, req.Identity.Connection, req.Identity.Subject)
	if err != nil {
		return "", "", nil, nil, errors.Wrap(err, "Driver.IdentityIn()")
	}
	var (
		live       bool
		seats      []signin.DirectorySeat
		signUpRole string
	)
	if link != nil {
		source = sessioninfo.UserExistingLink
		tenant, live, stop, err = resolveLinked(ctx, d, tx, req, link, rules.ConfirmAddress)
	} else {
		var resolved unlinkedUser
		resolved, stop, err = resolveUnlinked(ctx, d, tx, req, rules)
		source, tenant, seats, live, signUpRole = resolved.source, resolved.tenant, resolved.seats, resolved.live, resolved.signUpRole
	}
	if err != nil || stop != nil {
		return "", "", nil, stop, err
	}
	if live {
		seats = seatsBeyondInvitation(seats, req.Invitation)
		// An invitation carries its own roles: they are what the person gets.
		signUpRole = ""
	}
	seated, err := seatDeclared(ctx, tx, req.UserID, req.Username, seats, rules.Seat)
	if err != nil {
		return "", "", nil, nil, err
	}
	committed.add(seated)
	enrolled, err := seatSignedUp(ctx, tx, req.UserID, req.Username, rules.Method, signUpRole, rules.SignUp)
	if err != nil {
		return "", "", nil, nil, err
	}
	committed.add(enrolled)
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

// resolveLinked resolves an identity that is linked to a user, link: the user is named
// on the request and the link's LastUsedAt is touched. An invitation the sign-in carries
// is consumed for the user, live reporting that it was, once its acceptance is decided
// where it rests on the code mailed to the invited address (acceptedByCode, under
// confirm), and not for a disabled user, who is refused once the
// sign-in is decided. tenant is the link's tenant key.
func resolveLinked[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, link *dbtype.SessionIdentity, confirm bool,
) (tenant string, live bool, stop, err error) {
	user, err := d.UserIn(ctx, tx, link.UserID)
	if err != nil {
		return "", false, nil, errors.Wrap(err, "Driver.UserIn()")
	}
	req.UserID, req.Username = link.UserID, user.Username
	if acceptsByCode(req, confirm) && !user.Disabled {
		if stop, _, err := acceptedByCode(ctx, d, tx, req.Invitation, user, invitedCodeWait(), nil); err != nil || stop != nil {
			return "", false, stop, err
		}
	}
	if err := d.TouchIdentityIn(ctx, tx, link.ID); err != nil {
		return "", false, nil, errors.Wrap(err, "Driver.TouchIdentityIn()")
	}
	if link.Tenant != nil {
		tenant = *link.Tenant
	}
	if user.Disabled {
		return tenant, false, nil, nil
	}
	live, err = consumeInvitation(ctx, d, tx, req, provenAddress(req, confirm) != "")
	if err != nil {
		return "", false, nil, err
	}

	return tenant, live, nil, nil
}

// unlinkedUser is how an identity linked to no user resolved: the user's source and
// the tenant key for the link, the default seats the user is granted in the transaction
// that makes the link, whether the invitation the sign-in carries is live, and the
// default role of the open method that admitted a person no user was created for, ""
// for none.
type unlinkedUser struct {
	source     sessioninfo.UserSource
	tenant     string
	seats      []signin.DirectorySeat
	live       bool
	signUpRole string
}

// resolveUnlinked resolves an identity that is linked to no user: the account rules
// decide it and their answer is applied. An acceptance that rests on the code mailed to
// the invited address (acceptsByCode) waits for it first, with nothing read or written.
// The sign-in that completes a wait applies an answer for the wait's user and no other
// (waitedOnAnother).
func resolveUnlinked[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (resolved unlinkedUser, stop, err error) {
	if acceptsByCode(req, rules.ConfirmAddress) && !req.Invitation.AddressProven {
		// Nobody is made or linked before the code mailed to the invited address is
		// entered. Once it is, the rules know the person by that address
		// (provenAddress).
		return unlinkedUser{}, invitedCodeWait(), nil
	}
	decided, stop, err := decideAccount(ctx, d, tx, req, rules)
	if err != nil || stop != nil {
		return unlinkedUser{}, stop, err
	}
	if stop := waitedOnAnother(rules.WaitedOn, decided.res); stop != nil {
		return unlinkedUser{}, stop, nil
	}
	source, tenant, live, stop, err := applyResolution(ctx, d, tx, req, decided, rules)
	if err != nil || stop != nil {
		return unlinkedUser{}, stop, err
	}
	resolved = unlinkedUser{source: source, tenant: tenant, live: live}
	// The seats follow the link: given where it is made, to a new user and to one who
	// stood before alike.
	if source == sessioninfo.UserProvisioned || source == sessioninfo.UserNewLink {
		resolved.seats = decided.res.Seats()
	}
	// The default role follows the account: given to the person the open method made
	// one for, never to one whose account stood before.
	if source == sessioninfo.UserProvisioned {
		resolved.signUpRole = decided.signUpRole
	}

	return resolved, nil, nil
}

// waitedOnAnother refuses the completion of a wait whose identity the account rules no
// longer give to the user the wait was for (Rules.WaitedOn). The step the person
// answered was that user's: the code went to the address their account holds, or their
// password confirmed the link. The rules decide the identity again at the completion,
// and the name it asserts may have moved to another account during the wait, so their
// answer is held to the wait's user here, before anything is written: a link to
// another user, a confirmation by another user and an account made new are refused,
// and no link or account is left behind. It returns nil for a sign-in that completes
// no wait, and for an answer that refuses the identity itself.
func waitedOnAnother(waitedOn impulse.NullUUID, res *signin.Resolution) error {
	if !waitedOn.Valid || res == nil {
		return nil
	}
	switch res.Outcome() {
	case signin.LinkIdentity, signin.RequireConfirmation:
		if res.UserID() == waitedOn.UUID {
			return nil
		}
	case signin.ProvisionUser:
		// A new account is nobody the wait was for.
	case signin.RejectIdentity, signin.ApplyStandardRule:
		return nil
	}

	return Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrIdentityRejected, "the identity no longer resolves to the user it was pending for")
}

// accountDecision is what the account rules answered for an unlinked identity: the
// resolution to apply, whether the invitation the sign-in carries is what admitted the
// person, the default role of the open method that admitted them, "" when no open
// method did or it names none, and whether a link rests on the name alone: the
// standard rule matched a user created ahead by the name, or the address, the identity
// asserts, and nothing else ties the two (linkUser). A custom rule's own answer rests on
// the rule's own evidence.
type accountDecision struct {
	res          *signin.Resolution
	byInvitation bool
	signUpRole   string
	onName       bool
}

// decideAccount asks the account rules what to do with an unlinked identity: the
// custom rule when the registration has one, and the standard rule when it has none or
// the custom rule answers signin.Standard. A refusal the standard rule makes before any
// row is written is returned as stop. The standard rule's tenant key is the
// organization the method names, or the identity's connection when it names none,
// unless the custom rule's Standard names another.
func decideAccount[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, rules Rules[Tx],
) (decided accountDecision, stop, err error) {
	tenant := req.Identity.Organization
	if tenant == "" {
		tenant = req.Identity.Connection
	}
	if rule := rules.Account; rule != nil {
		res, err := rule(ctx, tx, req)
		if err != nil {
			return accountDecision{}, nil, errors.Wrap(err, "signin.AccountRule()")
		}
		if res == nil || res.Outcome() != signin.ApplyStandardRule {
			return accountDecision{res: res}, nil, nil
		}
		if res.Tenant() != "" {
			tenant = res.Tenant()
		}
	}

	return standardRule(ctx, d, tx, req, tenant, rules)
}

// standardRule applies the library's standard rule to an unlinked identity, inside tx.
// It refuses a guest of the directory, a token asserting no application role when the
// method synchronizes roles, and an identity without a sign-in name, before any row is
// written. A pre-created user under the sign-in name, matched without regard to case,
// or one who holds the identity's verified address (preCreatedUser), is confirmed with
// its password when it has one and linked when it has none; where the sign-in rests on
// the code mailed to an invited address, a user that address does not make the invited
// one is refused first, password or none (notTheInvited). (applyResolution refuses
// the link when such a user already signs in through another connection). The link is
// the person's first sign-in through the directory, so the tenant whose directory it is
// gives them its default seat with it, when the application reads the declarations
// (rules.Directories): at once for a user linked here, and once the password has
// confirmed the link for a user who has one (LinkConfirmed). A person no user was
// created for goes to the tenant's declaration of the directory first (see
// declaredRule), and otherwise to what an administrator chose for the method
// (methodRule).
func standardRule[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, tenant string, rules Rules[Tx],
) (decided accountDecision, stop, err error) {
	switch {
	case req.Identity.Guest:
		return accountDecision{}, Refusal("", sessioninfo.RefusedGuestAccount, signin.ErrGuestAccount, "guest accounts are not members of the directory"), nil
	case req.RoleNames != nil && len(req.RoleNames) == 0:
		return accountDecision{}, Refusal("", sessioninfo.RefusedNoRoles, signin.ErrNoRoles, "Unauthorized: user has no roles"), nil
	case req.Identity.SignInName == "":
		return accountDecision{}, Refusal("", sessioninfo.RefusedNoEmail, signin.ErrNoSignInName, "the identity carries no sign-in name"), nil
	}

	proven := provenAddress(req, rules.ConfirmAddress)
	user, err := preCreatedUser(ctx, d, tx, req.Identity, proven)
	if err != nil {
		return accountDecision{}, nil, err
	}
	user = asRegistered(user, rules)
	if user != nil && user.HasPassword {
		// An account the invited address does not make the invited one is refused before
		// its password is asked for, as one without a password is when it is linked.
		if stop, err := notTheInvited(ctx, d, tx, req.Invitation, proven, user); err != nil || stop != nil {
			return accountDecision{}, stop, err
		}

		return accountDecision{res: signin.Confirm(user.ID, signin.Tenant(tenant))}, nil, nil
	}

	declared, err := declaredDirectory(ctx, tx, req.Identity, rules.Directories)
	if err != nil {
		return accountDecision{}, nil, err
	}
	switch {
	case user != nil:
		// Nothing but the name, or the address, ties the account to the identity.
		res := signin.Link(user.ID, false, signin.Tenant(tenant), signin.Seats(defaultSeats(declared)...))

		return accountDecision{res: res, onName: true}, nil, nil
	case len(declared) > 0:
		res, byInvitation, stop := declaredRule(req, declared, tenant)

		return accountDecision{res: res, byInvitation: byInvitation}, stop, nil
	}

	return methodRule(ctx, d, tx, req, tenant, rules.Method, rules.Audience)
}

// methodRule decides a person no user was created for, who came through no directory a
// tenant declares, by the sign-in method they came through.
//
//   - A method whose directory writes the roles (the request carries the roles the
//     token asserts) has no choice to read: the directory says who holds a role, the
//     token named at least one, and the person is provisioned under the sign-in name.
//   - A method an administrator opened to sign-up (its row of the sign-in methods
//     table, read here, in the sign-in's own transaction) admits the person: they are
//     provisioned, and given the default role the row names where it names one
//     (Rules.SignUp). The row counts for the audience it was opened for alone
//     (Rules.Audience): a registration changed since is invite only.
//   - Every other method is invite only, a method with no row among them. The
//     invitation the browser carries is the account: the person was invited by
//     address, and SignIn has already held the sign-in to the invited one (the address
//     the method verified, the code mailed to the invited address, or, where no code
//     is mailed, the address the directory asserts).
//     Without one the sign-in is refused (not_invited) and nothing is written.
//
// A person who carries an invitation through an open method is admitted by the method,
// so an invitation that has died meanwhile does not refuse them, and takes the
// invitation's roles where it is live (resolveIn).
func methodRule[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, tenant, method, audience string,
) (decided accountDecision, stop, err error) {
	provision := signin.Provision(&signin.NewUser{Username: req.Identity.SignInName}, nil, signin.Tenant(tenant))
	if req.RoleNames != nil {
		return accountDecision{res: provision}, nil, nil
	}
	var chosen *dbtype.SignInMethod
	if method != "" {
		if chosen, err = d.SignInMethodIn(ctx, tx, method); err != nil {
			return accountDecision{}, nil, errors.Wrap(err, "Driver.SignInMethodIn()")
		}
	}
	switch {
	case chosen.OpenFor(audience):
		return accountDecision{res: provision, signUpRole: chosen.Role()}, nil, nil
	case req.Invitation != nil:
		return accountDecision{res: provision, byInvitation: true}, nil, nil
	default:
		return accountDecision{}, notInvitedRefusal(), nil
	}
}

// NotInvitedMessage is the sentence a person reads when a sign-in method that is invite
// only refuses them: they hold no account, and carried no invitation.
const NotInvitedMessage = "this sign-in is by invitation: ask an administrator to invite you"

// seatSignedUp gives the default role of the open method that admitted a person, inside
// the resolution's transaction: seat, handed the transaction, and answers what it asks
// to have run once the transaction has committed. Nothing is given where the method
// names no role. A role on an Auth with no hook is a wiring mistake and fails the
// transaction, as a hook's error does: no account is made holding nothing.
func seatSignedUp[Tx any](
	ctx context.Context, tx Tx, userID impulse.UUID, username, method, role string, seat SignUpSeater[Tx],
) (committed func(ctx context.Context), err error) {
	if role == "" {
		return nil, nil
	}
	if seat == nil {
		return nil, errors.Newf("the sign-in method %q is open to sign-up with the default role %q and the Auth has no sign-up hook to give it: set it with SetSignUpSeated", method, role)
	}
	committed, err = seat(ctx, tx, userID, username, method, role)
	if err != nil {
		return nil, errors.Wrap(err, "SignUpSeater()")
	}

	return committed, nil
}

// declaredRule decides a person no user was created for, from a directory some
// tenant declares: a declared directory belongs to its tenant, and the tenant's
// declaration decides before the sign-in method does, open or not. A person with no user
// holds nothing: a role is held by an account, so the tenant's own person, given a
// role ahead of their first sign-in, has the account the role was given to, and the
// sign-in found it before it came here (preCreatedUser). A tenant that names a default
// seat admits a stranger: they are provisioned and seated in the tenant, in the
// resolution's transaction (Rules.Seat). When the tenant names no seat, it admits its
// people by invitation only: the invitation the browser carries is
// the account, as through a method that is invite only, and without one the sign-in is
// refused (invitation_only) and nothing is written.
func declaredRule(
	req *sessioninfo.NewSessionRequest, declared []signin.DeclaredDirectory, tenant string,
) (res *signin.Resolution, byInvitation bool, stop error) {
	seats := defaultSeats(declared)
	newUser := &signin.NewUser{Username: req.Identity.SignInName}
	switch {
	case len(seats) > 0:
		return signin.Provision(newUser, nil, signin.Tenant(tenant), signin.Seats(seats...)), false, nil
	case req.Invitation != nil:
		return signin.Provision(newUser, nil, signin.Tenant(tenant)), true, nil
	default:
		return nil, false, invitationOnlyRefusal()
	}
}

// declaredDirectory reads the declaration of the directory identity came through,
// through the application's reader, nil for none: the tenant whose directory it is,
// with its default seat. A directory belongs to one tenant, so the answer names one
// tenant at most; nothing is read, and nothing answered, for an identity that came
// through no organization's directory (a personal account), which no tenant declares.
func declaredDirectory[Tx any](ctx context.Context, tx Tx, identity *sessioninfo.Identity, read signin.DirectoryReader[Tx]) ([]signin.DeclaredDirectory, error) {
	directory := signin.Directory{Method: identity.Method, Connection: identity.Connection}
	if read == nil || !directory.Organization() {
		return nil, nil
	}
	declared, err := read(ctx, tx, directory)
	if err != nil {
		return nil, errors.Wrap(err, "signin.DirectoryReader()")
	}

	return declared, nil
}

// defaultSeats are the seats the declarations of a directory give a person at their
// first sign-in through it: the default seat of each tenant that names one.
func defaultSeats(declared []signin.DeclaredDirectory) []signin.DirectorySeat {
	seats := make([]signin.DirectorySeat, 0, len(declared))
	for _, d := range declared {
		if d.DefaultSeat != "" {
			seats = append(seats, signin.DirectorySeat{Tenant: d.Tenant, Role: d.DefaultSeat})
		}
	}

	return seats
}

// preCreatedUser finds the user an unlinked identity belongs to, before any row is
// written: the user under the identity's sign-in name, matched without regard to case,
// or else the user who holds the address the sign-in has shown to be the person's
// (knownHolder): verified, the one the method vouches for (Identity.EmailVerified:
// Google alone does, and only for a Gmail address or a Workspace account's; no other
// method's word counts), or, confirmed or not, proven, the invited address whose mailed
// code the sign-in entered. A person is one user whichever way they sign in, and an
// address counts once the person has proven it: an address the method did not verify
// matches no one, and neither does one the method verified that a user holds
// unverified. nil when there is neither.
func preCreatedUser[Tx any](ctx context.Context, d Driver[Tx], tx Tx, identity *sessioninfo.Identity, proven string) (*dbtype.User, error) {
	user, err := d.UserByUsernameIn(ctx, tx, identity.SignInName)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByUsernameIn()")
	}
	if user != nil {
		return user, nil
	}
	user, _, err = knownHolder(ctx, d, tx, identity, proven)

	return user, err
}

// applyResolution acts on the account rules' answer for an unlinked identity. A link
// or a new user sets req.UserID and req.Username and reports how the user was found
// and its tenant, and consumes the invitation the sign-in carries, if any, once the
// user is linked or about to be written (live reports that it was): a refusal or a
// pending confirmation, returned as stop, consumes nothing, and neither does a
// disabled user, who is refused once the sign-in is decided. decided.byInvitation says
// the invitation alone admitted the person, so a dead one is a refusal, and
// decided.onName that a link rests on the name alone, which linkUser decides under
// rules.
func applyResolution[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, decided accountDecision, rules Rules[Tx],
) (source sessioninfo.UserSource, tenant string, live bool, stop, err error) {
	res := decided.res
	if res == nil {
		res = signin.Reject("")
	}

	switch res.Outcome() {
	case signin.RejectIdentity:
		return "", "", false, Refusal(res.Refusal(), sessioninfo.RefusedIdentityRejected, signin.ErrIdentityRejected, "identity rejected"), nil
	case signin.LinkIdentity:
		live, stop, err = linkUser(ctx, d, tx, req, res, decided.onName, rules)
		if err != nil || stop != nil {
			return "", "", false, stop, err
		}

		return sessioninfo.UserNewLink, res.Tenant(), live, nil, nil
	case signin.ProvisionUser:
		live, stop, err = provisionUser(ctx, d, tx, req, res, decided.byInvitation, provenAddress(req, rules.ConfirmAddress))
		if err != nil || stop != nil {
			return "", "", false, stop, err
		}

		return sessioninfo.UserProvisioned, res.Tenant(), live, nil, nil
	case signin.RequireConfirmation:
		// A confirmation is the user's password, and an Auth with no password method
		// serves nothing that takes one: the sign-in would wait on what nobody can
		// give. It is refused, and never made a link, which would join the account on
		// less proof than the rule asked for.
		if rules.NoPassword {
			return "", "", false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrConfirmNeedsPasswordMethod, "identity requires a confirmation that cannot be given"), nil
		}
		user, err := d.UserIn(ctx, tx, res.UserID())
		if err != nil {
			return "", "", false, nil, errors.Wrap(err, "Driver.UserIn()")
		}

		return "", "", false, &signin.PendingSignInError{
			Reason: sessioninfo.PendingConfirmation, UserID: impulse.NullUUIDFromUUID(res.UserID()), Username: user.Username, Tenant: res.Tenant(),
		}, nil
	case signin.ApplyStandardRule:
		return "", "", false, nil, errors.New("the standard rule answered signin.Standard: the standard rule decides the identity itself")
	default:
		return "", "", false, nil, errors.Newf("unknown account rule outcome %d", res.Outcome())
	}
}

// linkUser links the identity to the user the rule named (signin.LinkIdentity), names
// the user on the request, and consumes the invitation the sign-in carries unless the
// user is disabled. An untrusted link to a user with a password, or from a connection
// a password-less user does not sign in through, is a refusal (stop) and links
// nothing. So is the standard rule's link, made on the name alone (onName), in two
// cases. A user with no password and no link is linked only when something vouches for
// it (firstLink): a tenant's directory or the user's own invitation in a tenanted
// application, and in any other the method's word for the name or the code mailed to
// the address the account holds, which the sign-in waits for (stop, a pending step)
// with nothing linked. A user with no password who already signs
// in through an identity is never linked a second one on the name: a name proves
// nothing (where a username is not an address, a person may hold an account under a
// colleague's sign-in name, link an identity of their own to it and remove its
// password), and such a user has no password to confirm with, so the colleague's
// sign-in would be linked into another person's account. A second identity is linked
// on proof: the user holds, verified, the address the method verified for the identity
// (holdsAddress), a user given a password confirms it, a registration's own rule links
// on its own evidence, and the application may link one itself.
func linkUser[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, res *signin.Resolution, onName bool, rules Rules[Tx],
) (live bool, stop, err error) {
	// The row is held until the transaction ends, so two link decisions for one
	// user run one after the other and the second sees the first's link.
	user, err := lockedAsRegistered(ctx, d, tx, res.UserID(), rules)
	if err != nil {
		return false, nil, err
	}
	if stop, err := notTheInvited(ctx, d, tx, req.Invitation, provenAddress(req, rules.ConfirmAddress), user); err != nil || stop != nil {
		return false, stop, err
	}
	if user.HasPassword && !res.Trusted() {
		return false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkRequiresConfirmation, "identity requires confirmation"), nil
	}
	byInvitation := false
	if !user.HasPassword && !res.Trusted() {
		links, err := d.IdentitiesByUserIn(ctx, tx, res.UserID())
		if err != nil {
			return false, nil, errors.Wrap(err, "Driver.IdentitiesByUserIn()")
		}
		switch {
		case len(links) == 0 && onName:
			byInvitation, stop, err = firstLink(ctx, d, tx, req, user, res.Tenant(), rules)
			if err != nil || stop != nil {
				return false, stop, err
			}
		case linksAnotherConnection(links, req.Identity):
			return false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkCrossConnection, "identity from another connection needs a trusted link"), nil
		case len(links) > 0 && onName:
			proven, err := holdsAddress(ctx, d, tx, user.ID, req.Identity)
			if err != nil {
				return false, nil, err
			}
			if !proven {
				return false, Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkSecondIdentity, "the account under this sign-in name signs in through another identity"), nil
			}
		}
	}
	if rules.ConfirmAddress {
		// An account that could not take the address the method vouches for is not
		// signed in to, and so is linked nothing.
		if stop, err = addressTaken(ctx, d, tx, req, user); err != nil || stop != nil {
			return false, stop, err
		}
	}
	if err := d.LinkIdentityIn(ctx, tx, res.UserID(), req.Identity, res.Tenant()); err != nil {
		return false, nil, errors.Wrap(err, "Driver.LinkIdentityIn()")
	}
	req.UserID, req.Username = res.UserID(), user.Username
	if user.Disabled {
		return false, nil, nil
	}
	live, err = consumeInvitation(ctx, d, tx, req, byInvitation || provenAddress(req, rules.ConfirmAddress) != "")

	return live, nil, err
}

// asRegistered is user as the sign-in's rules decide it, nil for nil: under an Auth that
// registers no password method (rules.NoPassword) a password the account holds counts
// for nothing, since no sign-in can prove it and no route takes its confirmation, so
// the account is decided as one with no password. The first link of such an account is
// then held to what vouches for any account created ahead of its sign-in (firstLink),
// and a second identity to the proof one needs (linkUser). The row is left as it is:
// the password is still the account's once the method is registered again.
func asRegistered[Tx any](user *dbtype.User, rules Rules[Tx]) *dbtype.User {
	if user == nil || !rules.NoPassword || !user.HasPassword {
		return user
	}
	decided := *user
	decided.HasPassword = false

	return &decided
}

// lockedAsRegistered holds the user's row until the transaction ends (Driver.LockUserIn)
// and returns the user as the sign-in's rules decide it (asRegistered).
func lockedAsRegistered[Tx any](ctx context.Context, d Driver[Tx], tx Tx, userID impulse.UUID, rules Rules[Tx]) (*dbtype.User, error) {
	user, err := d.LockUserIn(ctx, tx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.LockUserIn()")
	}

	return asRegistered(user, rules), nil
}

// holdsAddress reports whether the user holds, verified, the address the method verified
// for identity: the person proved the address to the application, and the directory
// proved it of whoever signs in, so the two are one person whatever name the account is
// under. An address the method did not verify, and one the user holds unverified or
// not at all, prove nothing. Neither does the code of an invitation's acceptance
// (provenAddress): it shows that the person reads the invited mailbox, and an account
// somebody already signs in to is not opened to whoever reads its mail.
func holdsAddress[Tx any](ctx context.Context, d Driver[Tx], tx Tx, userID impulse.UUID, identity *sessioninfo.Identity) (bool, error) {
	if !identity.EmailVerified || identity.Email == "" {
		return false, nil
	}
	holder, err := d.UserByVerifiedEmailIn(ctx, tx, identity.Email)
	if err != nil {
		return false, errors.Wrap(err, "Driver.UserByVerifiedEmailIn()")
	}

	return holder != nil && holder.ID == userID, nil
}

// firstLink decides the standard rule's link to a user who has no password and no link:
// an account that exists ahead of its first sign-in (one the application created with
// no password, a seed's), which nothing but its name ties to the person signing in. A
// custom rule's link rests on the rule's own evidence and does not come here, and a
// registration that declares its provider's names the application's own
// (rules.SeededUsers, signin.Registration.SeededUsers) links such a user on the name. In
// an application whose tenants have directories (rules.Directories), a directory
// asserts the names it likes, and an organization's own provider is not held to any
// domain, so the name alone links nothing. The link hands whoever the directory signs
// in everything the account holds, so it is made when something vouches for it:
//
//   - the sign-in came through the directory of the one tenant the user belongs to
//     (rules.OnlyIn): the user holds a role there and nothing anywhere else, so the
//     tenant's directory is given nothing the tenant's own administrators could not
//     give. A user who also holds a role in another tenant, in every tenant or outside
//     the tenants is not the tenant's alone, and a role the tenant's administrator
//     gives such a user changes nothing;
//   - the sign-in carries the user's own invitation (ownInvitation): the person holds
//     the link mailed to the account's address. byInvitation then says the invitation
//     admitted the link, so a dead one ends the sign-in and links nothing. A disabled
//     user accepts no invitation, so theirs vouches for nothing.
//
// The deployment's own first administrator is no such account: no account is made ahead
// of the person, who is invited, and the invitation is what vouches.
//
// Any other sign-in under the name is refused as a person no account was created for
// is, with that refusal's code and words, which say nothing of the account: by
// invitation only through a directory some tenant has, not invited through any other.
// An application with no tenant directories has only the directories its deployment
// registered for itself, and no tenant to vouch for a link: the link is made when the
// method vouches for the name and the name is the account's own, and otherwise waits for
// the code mailed to the address the account holds (nameAloneHold; tenant is the link's
// tenant key, which the wait carries).
func firstLink[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, user *dbtype.User, tenant string, rules Rules[Tx],
) (byInvitation bool, stop, err error) {
	if rules.SeededUsers {
		return false, nil, nil
	}
	if rules.Directories == nil {
		stop, err := nameAloneHold(ctx, d, tx, req, user, tenant, rules.CodeAddress)

		return false, stop, err
	}
	declared, err := declaredDirectory(ctx, tx, req.Identity, rules.Directories)
	if err != nil {
		return false, nil, err
	}
	if rules.OnlyIn != nil {
		for _, declaration := range declared {
			alone, err := rules.OnlyIn(ctx, tx, user.ID, declaration.Tenant)
			if err != nil {
				return false, nil, errors.Wrap(err, "OnlyInTenant()")
			}
			if alone {
				return false, nil, nil
			}
		}
	}
	own, err := ownInvitation(ctx, d, tx, user, req.Invitation)
	if err != nil {
		return false, nil, err
	}
	if own && !user.Disabled {
		return true, nil, nil
	}
	if len(declared) > 0 {
		return false, invitationOnlyRefusal(), nil
	}

	return false, notInvitedRefusal(), nil
}

// nameAloneHold decides the standard rule's first link to user, an account that stood
// before the sign-in with no password and no link, in an application with no tenant
// directories: nothing but the sign-in name ties the account to the person signing in.
// A name the method vouches for is the person's own: Google names an account by its
// address, and answers for a Gmail address and for a Workspace account's
// (Identity.EmailVerified). That link is made at once (nil) where the name is also the
// account's own: its username as the library compares two addresses (sameAddress), or
// the address the account holds confirmed (holdsAddress). The stores find a username
// more widely than that (they fold Unicode's case and normalization), so a vouched name
// that only reads like the account's (the Kelvin sign for a k) vouches for another
// mailbox than the one the account is under, and goes on as a name the method does not
// vouch for. Any other name is the provider's word alone. A Google account can be made
// under any address from outside Google, an identity provider's administrators give the
// names they like, and a Microsoft directory answers for no address; the link would
// hand whoever signs in everything the account holds. So the sign-in waits for the code
// mailed to the address the account holds, whether or not its person has confirmed it:
// the answer is a pending MFA step that takes the mailed code alone, and nothing is
// linked while it waits, so a sign-in that never finishes leaves the account as it was.
// Whoever reads the account's mail is the account's person, and the sign-in that carries
// the code's event is linked, while the account still holds the address the code was
// mailed to (claim, the rules' CodeAddress: Driver.UserByEmailIn answers this same
// user). An account whose address was changed or taken off since is refused with
// code_expired, the words of a code that no longer stands, and nothing is linked. A code
// mailed for an invitation carries no claim: the sign-in that entered it is linked only
// where the invitation is the account's own, mailed to its username or to the address it
// holds (ownInvitation), and is otherwise refused as an acceptance with another address
// is, with nothing linked. The code's event with neither links nothing (codeEventLink).
// An account that holds no address has nowhere to mail the code, and an address given at
// the sign-in would show only that the person reads their own mail, so the link is
// refused (signin.ErrLinkNeedsAddress).
func nameAloneHold[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, user *dbtype.User, tenant string, claim *dbtype.CodeAddress,
) (stop, err error) {
	identity := req.Identity
	if identity.EmailVerified && sameAddress(identity.SignInName, identity.Email) {
		own := sameAddress(identity.SignInName, user.Username)
		if !own {
			if own, err = holdsAddress(ctx, d, tx, user.ID, identity); err != nil {
				return nil, err
			}
		}
		if own {
			return nil, nil
		}
	}
	if slices.ContainsFunc(req.AuthEvents, func(e sessioninfo.AuthEvent) bool { return e.Method == sessioninfo.MethodEmailCode }) {
		if decided, stop, err := codeEventLink(ctx, d, tx, req.Invitation, user, claim); err != nil || decided {
			return stop, err
		}
	}
	if !user.EmailHeld {
		return Refusal("", sessioninfo.RefusedIdentityRejected, signin.ErrLinkNeedsAddress, "the account under this sign-in name holds no email address to mail a sign-in code to"), nil
	}

	return &signin.PendingSignInError{
		Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(user.ID), Username: user.Username, Tenant: tenant,
		Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
	}, nil
}

// codeEventLink decides the first link of user, made on a name alone, by a sign-in that
// carries the mailed code's event: the account's own code (claim) links while the
// account still holds the address it was mailed to, and is refused as a code that no
// longer stands otherwise; the code of the invitation the sign-in carries (inv, once
// Invitation.AddressProven) links only where the invitation is the account's own
// (ownInvitation), and is refused otherwise as an acceptance with another address is,
// with nothing linked and the invitation standing. decided is false for an event with
// neither, which counts for nothing.
func codeEventLink[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, inv *sessioninfo.Invitation, user *dbtype.User, claim *dbtype.CodeAddress,
) (decided bool, stop, err error) {
	switch {
	case claim != nil:
		holder, err := d.UserByEmailIn(ctx, tx, claim.Email)
		if err != nil {
			return true, nil, errors.Wrap(err, "Driver.UserByEmailIn()")
		}
		if holder == nil || holder.ID != user.ID {
			return true, codeAddressGone(user.ID, claim), nil
		}

		return true, nil, nil
	case inv != nil && inv.AddressProven:
		own, err := ownInvitation(ctx, d, tx, user, inv)
		if err != nil {
			return true, nil, err
		}
		if !own {
			return true, Refusal("", sessioninfo.RefusedInvitationAddress, signin.ErrInvitationAddress, InvitedAddressMessage(inv)), nil
		}

		return true, nil, nil
	default:
		return false, nil, nil
	}
}

// errCodeAddressGone is the cause of the refusal of a sign-in whose mailed code confirms
// an address the account no longer holds as it held it when the code was checked, or
// that another account holds by now: the code no longer stands, and the sign-in is
// refused as one with a code that expired is (code_expired), with nothing written and
// nothing linked by it.
var errCodeAddressGone = errors.New("the account no longer holds the address the sign-in code was mailed to")

// codeAddressGone is the refusal of a sign-in whose code confirms claim's address, which
// the user no longer holds or another user holds: the words of a code that no longer
// stands.
func codeAddressGone(userID impulse.UUID, claim *dbtype.CodeAddress) error {
	cause := fmt.Errorf("%w: user %s, the address %s", errCodeAddressGone, userID, claim.Email)

	return Refusal("", sessioninfo.RefusedCodeExpired, cause, "no sign-in code stands: ask for a new one")
}

// ownInvitation reports whether the invitation a sign-in carries was mailed to user's
// own address: the address is the user's username, or the user holds it, verified or
// not (an account that has never signed in has proven no address; the one it holds is
// the one it was created with). What ties the person signing in to the account is the
// proof of that address the sign-in has carried by the time it comes here: a claim its
// method verified, or the code mailed to the invited address (invitedAddressRefusal,
// resolveUnlinked). The link alone, which can be forwarded, ties nobody, and neither
// does an address the directory asserts without vouching for it.
func ownInvitation[Tx any](ctx context.Context, d Driver[Tx], tx Tx, user *dbtype.User, inv *sessioninfo.Invitation) (bool, error) {
	if inv == nil || inv.Email == "" {
		return false, nil
	}
	if sameAddress(inv.Email, user.Username) {
		return true, nil
	}
	holder, err := d.UserByEmailIn(ctx, tx, inv.Email)
	if err != nil {
		return false, errors.Wrap(err, "Driver.UserByEmailIn()")
	}

	return holder != nil && holder.ID == user.ID, nil
}

// invitationOnlyRefusal is the refusal of a person no account was created for who
// comes through a directory whose tenant admits its people by invitation only, and
// notInvitedRefusal that of one a sign-in method that is invite only refuses.
func invitationOnlyRefusal() error {
	return Refusal("", sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly, "this organization signs people in by invitation: ask your administrator to invite you")
}

func notInvitedRefusal() error {
	return Refusal("", sessioninfo.RefusedNotInvited, signin.ErrNotInvited, NotInvitedMessage)
}

// provisionUser writes the user the rule provisioned (signin.ProvisionUser), linked to
// the identity, and runs the rule's OnProvisioned. A sign-in whose known address (the
// identity's verified one, or proven, the invited address whose mailed code it entered)
// another user holds verified is refused instead (stop; see addressHeld). The
// invitation the sign-in carries is consumed before the row is written, so a dead one
// gives the new user nothing, not its address and not its grant, and a live one's
// address goes on the row (invitedAddress); a user provisioned disabled cannot accept
// and consumes nothing.
func provisionUser[Tx any](
	ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, res *signin.Resolution, byInvitation bool, proven string,
) (live bool, stop, err error) {
	newUser := res.NewUser()
	if newUser == nil {
		return false, nil, errors.New("the account rule provisioned no user: signin.Provision was given nil")
	}
	if stop := notTheInvitedName(d, req.Invitation, proven, newUser.Username); stop != nil {
		return false, stop, nil
	}
	if stop, err = addressHeld(ctx, d, tx, req.Identity, proven); err != nil || stop != nil {
		return false, stop, err
	}
	req.Username = newUser.Username
	// The new account carries the name the sign-in asserts, since a row buffered in this
	// transaction cannot be updated in it.
	insert := &dbtype.InsertSessionUser{Username: newUser.Username, PasswordHash: newUser.PasswordHash, Disabled: newUser.Disabled, Name: keptName(req.Identity.Name)}
	if !newUser.Disabled {
		if live, err = consumeInvitation(ctx, d, tx, req, byInvitation || proven != ""); err != nil {
			return false, nil, err
		}
	}
	if live {
		if err := invitedAddress(ctx, d, tx, req, insert); err != nil {
			return false, nil, err
		}
		// The inviter's name for the person stands where the sign-in asserted none.
		if insert.Name == nil {
			insert.Name = keptName(req.Invitation.Name)
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

// addressHeld refuses the provisioning of a new user for a sign-in whose known address
// another user already holds (knownHolder: verified for the address the method
// verified, and confirmed or not for the invited address whose mailed code the sign-in
// entered): that user is the person. The standard rule matches the person by the
// address before it provisions (preCreatedUser), so a rule that still provisions would
// make a second user for one person, and one without an address, since the address is
// held. The refusal is identity_rejected with signin.ErrEmailHeld as its cause, which
// names the holder for the log. An address the method did not verify holds nothing
// against the provisioning, and neither does one it verified that a user holds
// unverified.
func addressHeld[Tx any](ctx context.Context, d Driver[Tx], tx Tx, identity *sessioninfo.Identity, proven string) (stop, err error) {
	holder, address, err := knownHolder(ctx, d, tx, identity, proven)
	if err != nil || holder == nil {
		return nil, err
	}
	cause := fmt.Errorf("%w: the address %s belongs to user %s (%s)", signin.ErrEmailHeld, address, holder.Username, holder.ID)

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
		insert.EmailVerified = util.EqualASCIIFold(strings.TrimSpace(insert.Username), strings.TrimSpace(inv.Email))

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

// keptName is a name as a new user row takes it: nil for none, and for one the store
// would refuse (too long to keep, or not one line of text), which is dropped so the
// sign-in it came with stands.
func keptName(name string) *string {
	kept := sessioninfo.KeptName(name)
	if kept == "" {
		return nil
	}

	return &kept
}

// claimInvitedName writes the name an invitation carries on the existing user who
// accepts it, inside the acceptance's transaction, when the user holds none.
func claimInvitedName[Tx any](ctx context.Context, d Driver[Tx], tx Tx, userID impulse.UUID, inv *sessioninfo.Invitation) error {
	if inv.Name == "" {
		return nil
	}
	if _, err := d.ClaimUserNameIn(ctx, tx, userID, inv.Name); err != nil {
		return errors.Wrap(err, "Driver.ClaimUserNameIn()")
	}

	return nil
}

// decideAndInsert is the second transaction of SignIn. It reads req's user, sets
// req.User and decides the sign-in. When the sign-in goes ahead it inserts the session.
// A refusal or an MFA wait is returned after the policy's own writes commit.
//
// A sign-in that rests on a password proof (proven) goes on only while the user still
// holds the password that was proved, read in this transaction, the one that inserts
// the session: a password changed, reset or removed since the proof ends the
// transaction with dbtype.ErrPasswordChanged before the policy is asked, with nothing
// written, so a session is never inserted on the strength of a password the user no
// longer holds. The nil proof holds the sign-in to nothing.
//
// The address the account's own mailed code confirms (rules.CodeAddress) is written here,
// in the transaction that inserts the session, and only when the sign-in goes ahead: the
// user is enabled, the password unchanged, the policy has allowed it and neither
// addressHold nor addressTaken stopped it. It is written only while the account still
// holds what it held when the code was checked and no other account holds the address
// (Driver.VerifyUserEmailIfHeldIn); otherwise the code no longer stands, and the sign-in
// is refused with code_expired and no session. A user who already holds a confirmed
// address is written nothing. A refusal or a wait writes no address, so the person signs
// in again and is asked for the code again.
func decideAndInsert[Tx any](
	ctx context.Context, d Driver[Tx], id impulse.UUID, insertSession *dbtype.InsertSession, req *sessioninfo.NewSessionRequest,
	source sessioninfo.UserSource, tenant string, rules Rules[Tx], trusted bool,
) error {
	policy, proven, confirmAddress, claim := rules.Policy, rules.Proven, rules.ConfirmAddress, rules.CodeAddress
	base := *req
	var v verdict
	err := d.Transact(ctx, func(ctx context.Context, tx Tx) error {
		// Each run starts from the resolved request.
		*req, v = base, verdict{}

		user, err := d.UserIn(ctx, tx, req.UserID)
		if err != nil {
			return errors.Wrap(err, "Driver.UserIn()")
		}
		if err := proven.Check(user.PasswordProof); err != nil {
			return errors.Wrap(err, "dbtype.PasswordProof.Check()")
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
		if confirmAddress && v.proceeds() {
			v = addressHold(req, user, claim)
		}
		if confirmAddress && v.proceeds() {
			if v.stop, err = addressTaken(ctx, d, tx, req, user); err != nil {
				return err
			}
		}
		if v.proceeds() && claim != nil && !user.EmailConfirmed {
			wrote, err := d.VerifyUserEmailIfHeldIn(ctx, tx, user.ID, claim.Held, claim.Email)
			if err != nil {
				return errors.Wrap(err, "Driver.VerifyUserEmailIfHeldIn()")
			}
			if !wrote {
				v = verdict{stop: codeAddressGone(user.ID, claim)}
			}
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

// addressHold holds a sign-in the rules let through for the code that confirms the
// person's address: a sign-in through an external method whose directory does not vouch
// for the address it asserts (Microsoft Entra ID vouches for none), by a person whose
// account holds no confirmed address. Every account a person signs in to has a confirmed
// address, so such a person's first sign-in waits for the code the library mails,
// whatever the sign-in rule says about a second factor, and the address the code
// confirms is written on the account as the session starts. Nothing stands in for it: a
// directory that attests to a second factor says nothing of the address, and no browser
// is remembered for a person who has confirmed none. The sign-in that completes the wait
// with the account's own code carries the address the code confirms (claim, the rules'
// CodeAddress) and goes on, and decideAndInsert writes it; every later sign-in goes on
// too, since the account then holds the address confirmed. A sign-in that carries the
// code's event and no claim is held all the same: it was held again for another factor
// after its code, which wrote nothing, and it is asked for the code once more before a
// session starts on an account with no confirmed address. A sign-in whose method
// vouches for the address is not held: Google does, for a Gmail address and a Workspace
// account's, and the address is written on the account as the session starts. A Google
// account made with an address from anywhere else is held as another provider's person
// is. A password sign-in and a session the application starts are not held either:
// those accounts are given their address where they are made.
func addressHold(req *sessioninfo.NewSessionRequest, user *dbtype.User, claim *dbtype.CodeAddress) verdict {
	if user.EmailConfirmed || !dbtype.IsExternal(req.Identity) || !holdsForAddress(req.Reason) {
		return verdict{}
	}
	if req.Identity.EmailVerified && req.Identity.Email != "" {
		return verdict{}
	}
	if claim != nil {
		return verdict{}
	}
	pending := &signin.PendingSignInError{
		Reason: sessioninfo.PendingMFA, UserID: impulse.NullUUIDFromUUID(req.UserID), Username: user.Username, Factors: []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
	}
	if req.User != nil {
		pending.Tenant = req.User.Tenant
	}

	return verdict{stop: pending}
}

// addressTaken refuses a sign-in to an account that could not hold its address. A
// method that vouches for the address (Identity.EmailVerified) spares the sign-in the
// wait for the mailed code because the address is written on the account as the
// session starts (addressHold). Where another user holds that address, confirmed, it
// is written on nobody, and the person would be signed in to an account with no
// confirmed address: an account created ahead under the address as its sign-in name is
// reached so, by the name, beside the account that holds the address. Which of the two
// is the person's is an administrator's to settle, by removing the duplicate account
// or its sign-in name, so the sign-in is refused (identity_rejected, with
// signin.ErrAddressHeldByAnother as its cause, which names both users for the log) and
// the person is told what any sign-in the rules reject is told, which says nothing of
// the other account. linkUser asks before it links, so a first sign-in that is refused
// links nothing, and decideAndInsert asks for an identity linked before.
//
// It answers nil for a user who holds a confirmed address, for a session the rules
// hold for no address (holdsForAddress), for an identity the method does not vouch
// for, whose address the mailed code answers for, and where no other user holds the
// address confirmed: an address another user holds unconfirmed was only typed there,
// and refuses nobody.
func addressTaken[Tx any](ctx context.Context, d Driver[Tx], tx Tx, req *sessioninfo.NewSessionRequest, user *dbtype.User) (stop, err error) {
	identity := req.Identity
	if user.EmailConfirmed || !dbtype.IsExternal(identity) || !holdsForAddress(req.Reason) {
		return nil, nil
	}
	if !identity.EmailVerified || identity.Email == "" {
		return nil, nil
	}
	holder, err := d.UserByVerifiedEmailIn(ctx, tx, identity.Email)
	if err != nil {
		return nil, errors.Wrap(err, "Driver.UserByVerifiedEmailIn()")
	}
	if holder == nil || holder.ID == user.ID {
		return nil, nil
	}
	cause := fmt.Errorf("%w: user %s (%s) holds the verified address %s, and the sign-in reached user %s (%s)",
		signin.ErrAddressHeldByAnother, holder.Username, holder.ID, identity.Email, user.Username, user.ID)

	return Refusal("", sessioninfo.RefusedIdentityRejected, cause, "identity rejected"), nil
}

// holdsForAddress reports whether a session created for reason is one a person signs in
// with: a login, a link a password confirmed, and the completion of a wait. A session
// that continues another was held, if it was to be, when that one was established.
func holdsForAddress(reason sessioninfo.NewSessionReason) bool {
	switch reason {
	case sessioninfo.ReasonLogin, sessioninfo.ReasonIdentityLinked, sessioninfo.ReasonStepUp:
		return true
	default:
		return false
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

// linksAnotherConnection reports whether identity would attach a new connection to a
// user who signs in through links already: none of them is on identity's (Method,
// Connection). A user without a password cannot confirm a link, so an untrusted link of
// a connection the user does not sign in through yet is refused: the rule's evidence is
// the only check, and a custom rule must say so with trusted. A connection the user
// already signs in through, however many others they have, adds nothing and is not
// checked here. Where the standard rule matched the user on the name alone, linkUser
// holds the link to more: a user with no link at all is firstLink's to decide, and one
// who signs in through an identity already is linked no second one unless they hold
// the identity's address verified.
func linksAnotherConnection(links []*dbtype.SessionIdentity, identity *sessioninfo.Identity) bool {
	for _, link := range links {
		if link.Method == identity.Method && link.Connection == identity.Connection {
			return false
		}
	}

	return len(links) > 0
}
