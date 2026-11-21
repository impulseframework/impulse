package session

import (
	"context"
	"net/http"
	"slices"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/attempts"
	"github.com/impulseframework/impulse/session/internal/sessioncore"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// ValidateSession validates the session cookie and stores the session data and its
// user (sessioninfo.UserFromCtx) in the context. Like Auth.ValidateSession, it
// refuses a session whose user is missing or disabled as unauthorized, and the request
// of a user who must change their password as forbidden, with a
// *sessioninfo.LoginRefusal carrying RefusedPasswordChangeRequired.
func (p *AuthAPI[S, U, Tx]) ValidateSession(ctx context.Context) (context.Context, error) {
	return p.auth.validateRequest(ctx)
}

// ValidateCredentials checks a username and password and returns the user ID. A
// password-less user always fails validation, and a disabled user is refused as at the
// login (signin.ErrUserDisabled): where a store wraps the library's, the user is
// disabled if either the store's answer or the library's own row says so. It starts no
// session and asks no sign-in policy. It is limited as the login is: a failure counts against the name,
// and a name whose attempts reached the limit is refused with a
// *sessioninfo.LoginRefusal carrying RefusedTooManyAttempts and the wait
// (sessioninfo.RetryAfterOf), before the user is looked up or the password hashed.
// The call has no client, so only the name is counted.
func (p *AuthAPI[S, U, Tx]) ValidateCredentials(ctx context.Context, username, password string) (impulse.UUID, error) {
	user, _, err := p.auth.checkCredentials(ctx, username, password, attempts.Client{})
	if err != nil {
		return impulse.NilUUID, err
	}

	return user.ID, nil
}

// StartAuthenticatedSession starts a session for an existing user the application
// authenticated itself, recording how in events, which must name at least one method:
// a sign-up that just created the user, a factor the application ran on its own. It is
// decided like a login: the sign-in policy, when one is set, answers with the request's
// ReasonExternalAuth, and the library refuses a missing or disabled user. An Allow
// establishes the session under a new session ID; a Deny is a refusal
// (sessioninfo.LoginRefusal); a RequireMFA holds the sign-in as the browser's pending
// identity and returns a *signin.PendingSignInError, as CompletePending does, for
// CompletePending to finish. There is no session start that skips the policy: a support
// session is StartImpersonatedSession.
//
// An event with a zero At records the session's creation time. Optional customData
// (at most one *S) is written atomically with the session insert, and the custom
// session data resolver is not called for it; without it the resolver receives
// ReasonExternalAuth.
func (p *AuthAPI[S, U, Tx]) StartAuthenticatedSession(ctx context.Context, w http.ResponseWriter, userID impulse.UUID, events []sessioninfo.AuthEvent, customData ...*S) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(customData) > 1 {
		return impulse.NilUUID, errors.New("at most one customData value may be provided; it is the complete custom session data row")
	}
	if len(events) == 0 {
		return impulse.NilUUID, httpio.NewBadRequestMessage("StartAuthenticatedSession records how the user authenticated: pass at least one auth event")
	}
	if err := validateEvents(events); err != nil {
		return impulse.NilUUID, err
	}

	user, err := p.auth.storage.User(ctx, userID)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}

	at := &signInAttempt{
		identity: &sessioninfo.Identity{Method: sessioninfo.MethodApplication, Subject: user.ID.String()},
		reason:   sessioninfo.ReasonExternalAuth,
		userID:   user.ID,
		username: user.Username,
		events:   events,
		sameSite: sessioncore.SameSiteStrict,
	}
	if len(customData) == 1 && customData[0] != nil {
		at.customData = customData[0]
	}
	outcome, err := p.auth.signIn(ctx, w, at)
	if err != nil {
		return impulse.NilUUID, err
	}
	if outcome.pending != nil {
		return impulse.NilUUID, stillPending(outcome.pending)
	}

	return outcome.sessionID, nil
}

// startSession inserts req's session and writes its cookies. It is for sessions the
// application or a user operation starts, with no identity to resolve.
func (a *Auth[S, U, Tx]) startSession(ctx context.Context, w http.ResponseWriter, req *sessioninfo.NewSessionRequest) (impulse.UUID, error) {
	id, err := sessioncore.EstablishSession(ctx, w, a.baseSession, sessioncore.SameSiteStrict, func(ctx context.Context) (impulse.UUID, error) {
		id, err := a.storage.CreateSession(ctx, req)
		if err != nil {
			return impulse.NilUUID, errors.Wrap(err, "sessionstorage.AuthStore.CreateSession()")
		}

		return id, nil
	})
	if err != nil {
		return impulse.NilUUID, err
	}

	sessioncore.LogSessionStarted(ctx, req.Username, id)

	return id, nil
}

// Logout destroys the current session.
func (p *AuthAPI[S, U, Tx]) Logout(ctx context.Context) error {
	return p.auth.shared().Logout(ctx)
}

// CreateSessionUser creates a user. A nil req.Password creates a password-less
// user. req.PasswordChangeRequired marks the password as one an operator set, which
// the user changes before anything else is served; a user without a password has
// nothing to change, so the mark without a password is a BadRequest. req.Email, when
// given, is written unverified and reserves the address; one another user holds is a
// Conflict. req.Name, when given, is the person's name, kept without the space around
// it; one too long to keep is a BadRequest.
func (p *AuthAPI[S, U, Tx]) CreateSessionUser(ctx context.Context, req *CreateUserRequest, customData ...*U) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if len(customData) > 1 {
		return impulse.NilUUID, errors.New("at most one customData value may be provided; it is the complete custom user data row")
	}
	if req.PasswordChangeRequired && req.Password == nil {
		return impulse.NilUUID, httpio.NewBadRequestMessage("a password change can only be required of a user that has a password")
	}
	if req.ID.Valid && req.ID.UUID == impulse.NilUUID {
		return impulse.NilUUID, errors.New("the id a user is created under is the nil id, which names nobody: leave ID null for a generated id")
	}

	var hash *securehash.Hash
	if req.Password != nil {
		var err error
		if hash, err = p.auth.hashToSet(*req.Password); err != nil {
			return impulse.NilUUID, err
		}
	}

	// Explicit conversion so a typed nil *U never crosses as a non-nil any.
	var data any
	if len(customData) == 1 && customData[0] != nil {
		data = customData[0]
	}

	email, err := optionalAddress(req.Email)
	if err != nil {
		return impulse.NilUUID, err
	}
	user, err := p.auth.storage.CreateUser(ctx, &sessionstorage.InsertSessionUser{
		ID: req.ID, Username: req.Username, PasswordHash: hash, Disabled: req.Disabled, PasswordChangeRequired: req.PasswordChangeRequired, Email: email, Name: req.Name,
	}, data)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "sessionstorage.AuthStore.CreateUser()")
	}

	return user.ID, nil
}

// Users lists every user, by username. A user carries its username, whether it is
// disabled, its password hash (nil for a password-less user), whether it must change
// its password, when it was created and when it last signed in.
func (p *AuthAPI[S, U, Tx]) Users(ctx context.Context) ([]*sessionstorage.SessionUser, error) {
	users, err := p.auth.storage.Users(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.Users()")
	}

	return users, nil
}

// User returns a user by ID, or a NotFound error.
func (p *AuthAPI[S, U, Tx]) User(ctx context.Context, userID impulse.UUID) (*sessionstorage.SessionUser, error) {
	user, err := p.auth.storage.User(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}

	return user, nil
}

// UserByUsername returns a user by username, matched without regard to case, or a
// NotFound error.
func (p *AuthAPI[S, U, Tx]) UserByUsername(ctx context.Context, username string) (*sessionstorage.SessionUser, error) {
	user, err := p.auth.storage.UserByUserName(ctx, username)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.UserByUserName()")
	}

	return user, nil
}

// ChangeSessionUserUsername changes a user's username, and atomically the username
// on its live sessions.
func (p *AuthAPI[S, U, Tx]) ChangeSessionUserUsername(ctx context.Context, userID impulse.UUID, username string) error {
	if err := p.auth.storage.SetUserUsername(ctx, userID, username); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.SetUserUsername()")
	}

	return nil
}

// ChangeSessionUserPassword changes a user's password to one the user chose and
// regenerates its session.
//
// The old password must match and the new one must differ from it (BadRequest
// otherwise). The old password is checked under the attempt limiter, counted under the
// user's name as a login is: a wrong one counts, and a name whose attempts reached the
// limit is refused with a *sessioninfo.LoginRefusal carrying RefusedTooManyAttempts and
// the wait (sessioninfo.RetryAfterOf), before anything is hashed. The call has no
// client, so only the name is counted; the password method's ChangeUserPassword handler
// counts the request's client too. The store is handed the hash the old password
// matched and writes the new one only while the user still holds it: a password that was
// changed, reset or removed after this call read the user stands as it is, and the call
// answers a Conflict that says to try again, with nothing written and no session
// destroyed. The store writes the password, clears the password change mark and
// destroys every session of the user, by UserId, in one transaction
// (AuthStore.ChangeUserPassword): the password is changed and the sessions ended
// together, or neither is. The caller then continues in a new session
// (ReasonRegeneration) that carries the auth events of the session the change was made
// in, its establishment and its factors (continuedAuthEvents); a call with no session
// of the user's own in the context starts one that records a password event. The new
// password is held to the rule every password is (acceptedPassword).
func (p *AuthAPI[S, U, Tx]) ChangeSessionUserPassword(ctx context.Context, w http.ResponseWriter, userID impulse.UUID, req *ChangeSessionUserPasswordRequest) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return p.auth.changeOwnPassword(ctx, w, userID, req, attempts.Client{})
}

// changeOwnPassword implements ChangeSessionUserPassword for client.
func (a *Auth[S, U, Tx]) changeOwnPassword(ctx context.Context, w http.ResponseWriter, userID impulse.UUID, req *ChangeSessionUserPasswordRequest, client attempts.Client) error {
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if wrong, err := a.provePassword(ctx, user, req.OldPassword, client); wrong {
		return httpio.NewBadRequestMessageWithError(err, "Old password incorrect")
	} else if err != nil {
		return err
	}
	if req.NewPassword == req.OldPassword {
		return httpio.NewBadRequestMessage("The new password must differ from the old one")
	}

	hash, err := a.hashToSet(req.NewPassword)
	if err != nil {
		return err
	}
	// One store call writes the password and ends the user's sessions, in one
	// transaction: no failure leaves the password changed and a session alive.
	if err := a.storage.ChangeUserPassword(ctx, user.ID, user.PasswordHash, hash); err != nil {
		if errors.Is(err, sessionstorage.ErrPasswordChanged) {
			return httpio.NewConflictMessageWithError(err, "Your password has changed since you entered it, so this change was not made: try again with your current password")
		}

		return errors.Wrap(err, "sessionstorage.AuthStore.ChangeUserPassword()")
	}
	a.clearAttempts(ctx, user.Username)
	// A reset link mailed for the password before does not reset the new one.
	a.endResetLinks(ctx, user.ID)

	// The regenerated session keeps the provider's session id, and its issuer, of the
	// session it replaces, so a provider-initiated logout still finds it.
	req2 := &sessioninfo.NewSessionRequest{
		Reason:     sessioninfo.ReasonRegeneration,
		Username:   user.Username,
		UserID:     user.ID,
		AuthEvents: continuedAuthEvents(ctx, user.ID),
	}
	if current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); ok && current != nil {
		req2.ProviderSessionID = current.ProviderSessionID
		req2.ProviderSessionIssuer = current.ProviderSessionIssuer
	}
	_, err = a.startSession(ctx, w, req2)

	return err
}

// continuedAuthEvents are the auth events of the session a person holds after their own
// password change: the events of the session that made the change, as the store holds
// them and the request's validation read them, so the new session is that one continued,
// established the way it was and carrying the factors it carried. A person who came
// through their organization's directory still holds a directory's session, and a
// tenant's administrator who entered with a password and a second factor still carries
// the factor. Nothing of it comes from the request. A change made with no session of the
// person's own in the context (a call of the application's, a session that views as
// someone else, a request a bearer token stands in for, which has no session row, one
// that recorded no event) has nothing to continue, and the new session records what the
// change proved: the password.
func continuedAuthEvents(ctx context.Context, userID impulse.UUID) []sessioninfo.AuthEvent {
	current, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || current == nil || current.Impersonation != nil || current.Token != nil ||
		!current.UserID.Valid || current.UserID.UUID != userID || len(current.AuthEvents) == 0 {
		return []sessioninfo.AuthEvent{{Method: sessioninfo.MethodPassword}}
	}

	return slices.Clone(current.AuthEvents)
}

// SetSessionUserPassword sets a password the user chose, without the old one: the end
// of a flow of the application's own that proved who they are (a reset link, an
// enrollment). The password change mark is cleared, the user's sessions are
// destroyed and the name's sign-in attempts are reset. A password an operator chose
// is ResetSessionUserPassword.
func (p *AuthAPI[S, U, Tx]) SetSessionUserPassword(ctx context.Context, userID impulse.UUID, password string) error {
	return p.auth.replacePassword(ctx, userID, password, false)
}

// ResetSessionUserPassword sets a temporary password an operator chose: the user must
// change it before anything else is served (SessionUser.PasswordChangeRequired; see
// "Users and the forced password change" in the README), its sessions are destroyed
// and the name's sign-in attempts are reset.
func (p *AuthAPI[S, U, Tx]) ResetSessionUserPassword(ctx context.Context, userID impulse.UUID, temporary string) error {
	return p.auth.replacePassword(ctx, userID, temporary, true)
}

// RemoveSessionUserPassword removes a user's password: from then on the user signs in
// through their identity links alone, and a password sign-in under their name is
// refused as a wrong password is. A user keeps the password, their last means of
// sign-in (sessionstorage.ErrLastSignInMethod, Conflict), unless an identity linked to
// them can sign them in: a link counts when its method is a registration the login page
// offers and that registration still takes the link's connection (signsInThrough), so
// a link left from a method the Auth no longer offers, or from a tenant or a Workspace
// its registration no longer takes, does not, and neither does a user with no link at
// all. The links that count are named to the store, which removes the password only
// while one of them still stands, in the transaction that removes it, so an unlink
// asked at once cannot leave the user with neither. A user that holds no password is a
// BadRequest. The password change mark goes with the password, and the name's sign-in
// attempts are reset. The user's sessions stand, the ones the password established
// among them, as a removed identity link's do.
//
// It asks for no proof and removes whatever password the user holds: it is an
// administrator's removal, or the end of a flow of the application's own that proved who
// the person is. A person removes their own through the password method's
// RemoveUserPassword handler, which asks for the password first and removes that
// password and no other.
func (p *AuthAPI[S, U, Tx]) RemoveSessionUserPassword(ctx context.Context, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	a := p.auth
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	links, err := a.storage.IdentitiesByUser(ctx, user.ID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}
	standing := a.linksThatSignIn(links)
	if err := a.storage.RemoveUserPassword(ctx, user.ID, standing); err != nil {
		if errors.Is(err, sessionstorage.ErrLastSignInMethod) && len(links) > 0 && len(standing) == 0 {
			return httpio.NewConflictMessageWithError(err,
				"none of the identities linked to the user can sign them in any more, so the password is their only means of sign-in: link a directory sign-in the login page offers first")
		}

		return errors.Wrap(err, "sessionstorage.AuthStore.RemoveUserPassword()")
	}
	a.clearAttempts(ctx, user.Username)
	a.endResetLinks(ctx, user.ID)

	return nil
}

// HashPassword hashes a password with the password method's hasher and answers the
// hash as the users table's PasswordHash column holds it. It is for a caller that
// writes the column in a transaction of its own (the generated user management, which
// sets a temporary password beside its other writes); the session library's own
// writers (ResetSessionUserPassword, CreateSessionUser) hash for themselves. An empty
// password is refused: no password is an absent hash, never the hash of nothing.
func (p *AuthAPI[S, U, Tx]) HashPassword(password string) (string, error) {
	if password == "" {
		return "", httpio.NewBadRequestMessage("an empty password is not a password")
	}
	hash, err := p.auth.hashToSet(password)
	if err != nil {
		return "", err
	}
	text, err := hash.MarshalText()
	if err != nil {
		return "", errors.Wrap(err, "securehash.Hash.MarshalText()")
	}

	return string(text), nil
}

// replacePassword sets a new password for userID with its change mark (true for a
// password an operator set, false for one the user chose), destroys the user's sessions
// and resets the name's sign-in attempts. It follows no proof of the password it
// replaces, so it overwrites whatever the user holds, on purpose: the caller is an
// operator, or a flow that proved who the person is by other means, and its password is
// to stand whatever was changed a moment before.
func (a *Auth[S, U, Tx]) replacePassword(ctx context.Context, userID impulse.UUID, password string, changeRequired bool) error {
	user, err := a.storage.User(ctx, userID)
	if err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	hash, err := a.hashToSet(password)
	if err != nil {
		return err
	}
	// One store call writes the password and ends the user's sessions, in one
	// transaction: no failure leaves the new password in place and a session alive, on
	// the act meant to end them.
	if err := a.storage.ReplaceUserPassword(ctx, userID, hash, changeRequired); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.ReplaceUserPassword()")
	}
	a.clearAttempts(ctx, user.Username)
	// A reset link mailed for the password before does not reset the new one.
	a.endResetLinks(ctx, userID)

	return nil
}

// refuseImpersonatorsOwnAccount refuses a switch of an account, disabling, enabling or
// deleting it, on the account of the real person behind a session that acts as someone
// else: in such a session nobody is the subject of what they do. A person who acts as
// an administrator would otherwise enable their own account again, or remove it, with
// what the administrator may do. The session is read off the call's context, where
// ValidateSession put it, so a route an application wrote that calls one of the three
// is held to the rule with no line of its own. The rule lives here, in the session
// API's three, which every generated route calls. The store's own three beneath them
// (sessionstorage's DeactivateUser, ActivateUser and DeleteUser) are the bare writes
// and are not held to it: they are handed no session to read, so a route that calls
// the store's method in place of the session API's makes the switch unasked. A call
// that carries no session, a job of the application's own, is nobody's act on
// themselves and goes through, and so does a session started by a person of another
// application, who has no account here. act says what was asked, as the end of "you
// cannot". The sentence and the log line are the ones the generated user management
// answers with (sessioninfo.ImpersonatorRefusal).
func refuseImpersonatorsOwnAccount(ctx context.Context, userID impulse.UUID, act string) error {
	sess, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData)
	if !ok || sess == nil || sess.Impersonation == nil {
		return nil
	}
	if actor := sess.Impersonation.ActorID; !actor.Valid || actor.UUID != userID {
		return nil
	}
	line, refusal := sessioninfo.ImpersonatorRefusal(userID, act)
	logger.FromCtx(ctx).Info(line)

	return refusal
}

// DeactivateSessionUser disables a user and destroys its sessions. In a session that
// acts as someone else it refuses the account of the person who started the session
// (refuseImpersonatorsOwnAccount). In a session of the person's own, the caller must
// guard against self-deactivation.
func (p *AuthAPI[S, U, Tx]) DeactivateSessionUser(ctx context.Context, userID impulse.UUID) error {
	if err := refuseImpersonatorsOwnAccount(ctx, userID, "disable your own account"); err != nil {
		return err
	}
	if err := p.auth.storage.DeactivateUser(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.DeactivateUser()")
	}
	if err := p.auth.storage.DestroyUserSessions(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.DestroyUserSessions()")
	}

	return nil
}

// ActivateSessionUser enables a user. In a session that acts as someone else it refuses
// the account of the person who started the session (refuseImpersonatorsOwnAccount).
func (p *AuthAPI[S, U, Tx]) ActivateSessionUser(ctx context.Context, userID impulse.UUID) error {
	if err := refuseImpersonatorsOwnAccount(ctx, userID, "enable your own account"); err != nil {
		return err
	}
	if err := p.auth.storage.ActivateUser(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.ActivateUser()")
	}

	return nil
}

// DeleteSessionUser deletes a user with its identity links and sessions. The
// sessions are destroyed first, by UserId, so a failed delete leaves no live session
// behind. In a session that acts as someone else it refuses the account of the person
// who started the session (refuseImpersonatorsOwnAccount). In a session of the person's
// own, the caller must guard against self-deletion.
func (p *AuthAPI[S, U, Tx]) DeleteSessionUser(ctx context.Context, userID impulse.UUID) error {
	if err := refuseImpersonatorsOwnAccount(ctx, userID, "delete your own account"); err != nil {
		return err
	}
	if _, err := p.auth.storage.User(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.User()")
	}
	if err := p.auth.storage.DestroyUserSessions(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.DestroyUserSessions()")
	}
	if err := p.auth.storage.DeleteUser(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.DeleteUser()")
	}

	return nil
}

// DestroyUserSessions expires every session of a user.
func (p *AuthAPI[S, U, Tx]) DestroyUserSessions(ctx context.Context, userID impulse.UUID) error {
	if err := p.auth.storage.DestroyUserSessions(ctx, userID); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.DestroyUserSessions()")
	}

	return nil
}

// Identities lists a user's linked external identities.
func (p *AuthAPI[S, U, Tx]) Identities(ctx context.Context, userID impulse.UUID) ([]*sessionstorage.SessionIdentity, error) {
	identities, err := p.auth.storage.IdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, errors.Wrap(err, "sessionstorage.AuthStore.IdentitiesByUser()")
	}

	return identities, nil
}

// UnlinkIdentity removes an identity link. A user that holds no password keeps a means
// of sign-in: the unlink is refused (sessionstorage.ErrLastSignInMethod, Conflict)
// unless another identity linked to them can sign them in, by the judgment the removal
// of a password is held to (signsInThrough). The last link that counts is therefore not
// unlinked while only links that sign nobody in would remain: one left from a method
// the Auth no longer offers, or from a tenant or a Workspace its registration no longer
// takes. The store judges the user's other links in the transaction that unlinks, so
// two unlinks asked at once cannot take the user's last two between them. A user that
// holds a password may lose every link; the password of a user with no link that
// counts is refused the same way (RemoveSessionUserPassword).
func (p *AuthAPI[S, U, Tx]) UnlinkIdentity(ctx context.Context, identityID impulse.UUID) error {
	if err := p.auth.storage.UnlinkIdentity(ctx, identityID, p.auth.signsIn); err != nil {
		return errors.Wrap(err, "sessionstorage.AuthStore.UnlinkIdentity()")
	}

	return nil
}

// UpdateCustomSessionData updates the custom session data of an active session.
func (p *AuthAPI[S, U, Tx]) UpdateCustomSessionData(ctx context.Context, sessionID impulse.UUID, mutate func(data *S) error) error {
	return p.auth.shared().UpdateCustomSessionData(ctx, sessionID, mutate)
}

// CustomData returns the current session's custom data.
func (p *AuthAPI[S, U, Tx]) CustomData(ctx context.Context) (S, error) {
	return p.auth.shared().CustomData(ctx)
}

// CustomUserData returns a user's custom user data.
func (p *AuthAPI[S, U, Tx]) CustomUserData(ctx context.Context, userID impulse.UUID) (U, error) {
	return p.auth.shared().CustomUserData(ctx, userID)
}

// UpdateCustomUserData updates a user's custom user data.
func (p *AuthAPI[S, U, Tx]) UpdateCustomUserData(ctx context.Context, userID impulse.UUID, mutate func(data *U) error) error {
	return p.auth.shared().UpdateCustomUserData(ctx, userID, mutate)
}

// StartImpersonatedSession establishes a session that operates as the request's
// principal on behalf of its actor. The request is one the application's declaration
// made (Impersonations.Request for a person signed in here, Impersonations.RequestFor
// for a server-side caller), which has checked the kind's grant and set the session's
// mask: a request built by hand does not compile, and the zero value is refused.
//
// A user principal names the person by the id of their account and resolves against
// the users: a principal that is no id is a BadRequest, a missing user is an error and
// a disabled one is refused. An actor from another application (the request's
// ActorRealm) is refused when their name reads as the id of an account, and, for a
// role principal, when their name is already a user's username.
//
// A user principal's session belongs to the impersonated user (SessionData.UserID).
// A role principal's session belongs to no user (null UserID). The session records
// an impersonation auth event whose connection is the actor.
func (p *AuthAPI[S, U, Tx]) StartImpersonatedSession(ctx context.Context, w http.ResponseWriter, req *ImpersonationRequest, customData ...*S) (impulse.UUID, error) {
	established, err := req.established()
	if err != nil {
		return impulse.NilUUID, err
	}

	return p.auth.shared().StartImpersonatedSession(ctx, w, established, customData, sessioncore.UserIdentity(p.auth.storage))
}

// DestroyImpersonatedSessions expires every impersonated session started by actor.
func (p *AuthAPI[S, U, Tx]) DestroyImpersonatedSessions(ctx context.Context, actor string) error {
	return p.auth.shared().DestroyImpersonatedSessions(ctx, actor)
}

// ActiveImpersonations lists active impersonations matching q.
func (p *AuthAPI[S, U, Tx]) ActiveImpersonations(ctx context.Context, q *ImpersonationQuery) ([]*sessioninfo.Impersonation, error) {
	return p.auth.shared().ActiveImpersonations(ctx, q)
}

// DestroyImpersonatedSession expires one impersonated session.
func (p *AuthAPI[S, U, Tx]) DestroyImpersonatedSession(ctx context.Context, sessionID impulse.UUID) error {
	return p.auth.shared().DestroyImpersonatedSession(ctx, sessionID)
}

// EndImpersonation ends the current impersonated session, restoring the actor's
// session when possible.
func (p *AuthAPI[S, U, Tx]) EndImpersonation(ctx context.Context, w http.ResponseWriter) (restored bool, err error) {
	return p.auth.shared().EndImpersonation(ctx, w)
}
