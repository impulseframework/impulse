package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// How a session ends, as the session library keeps it. A session ends by its Expired
// column, stamped with the commit's time, and the row stays. A session established as
// someone or something else has an impersonation record beside it, and the record is the
// authority: the library refuses a session whose record has ended whatever the session
// row says. So every rule here that ends a session ends its record with it, with the
// reason the library gives an administrator's act (Revoked), and none deletes a row.

// revoked is the reason an impersonation record carries when an administrator ended it.
const revoked = string(sessioninfo.ImpersonationEndedByRevocation)

// EndSession buffers the end of one session and of its impersonation record, if it has
// one that still stands, in the caller's transaction: once it commits, the next request
// that carries the session is refused. A session that has ended already is left as it
// is, and a session that does not exist is refused.
//
// Shared rule: the session library ends a session and its record together
// (DestroySession, DestroyImpersonatedSession). A session established from this one, as
// someone else, is not ended with it, as it is not there.
func (m *Manager) EndSession(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	found, err := m.endSession(ctx, txn, sessionID)
	if err != nil {
		return err
	}
	if !found {
		return httpio.NewNotFoundMessagef("session %q does not exist", sessionID)
	}

	return nil
}

// EndImpersonation buffers the end of one impersonated session: the session and its
// record, as EndSession ends them. A session that was established as nobody else has no
// record and is refused, so the call cannot end a person's own session by mistake; a
// record that has ended already is left as it is.
//
// Shared rule: the session library's DestroyImpersonatedSession, which acts only where
// a record stands.
func (m *Manager) EndImpersonation(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	live, found, err := m.store.Impersonation(ctx, txn, sessionID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Impersonation()")
	}
	if !found {
		return httpio.NewNotFoundMessagef("session %q has no impersonation record", sessionID)
	}
	if !live {
		return nil
	}
	if _, err := m.endSessionAndRecord(ctx, txn, sessionID, live); err != nil {
		return err
	}

	return nil
}

// EndUserSessions buffers the end of every session a user holds, in the caller's
// transaction: the sessions that are the user's own, and the sessions the user
// established as someone or something else, which are found by the user's name in their
// records since such a session belongs to whoever it acts as. Every record of those
// sessions that still stands ends with them. A user that does not exist holds none.
//
// Shared rule: the session library's DestroyUserSessions. One thing it does is left to
// the library: a record still standing beside a session of the user that had ended
// before (the state a browser that never came back leaves) is ended there the next time
// that browser returns, and stands for nothing until then, since its session is refused.
func (m *Manager) EndUserSessions(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return m.endUserSessions(ctx, txn, userID)
}

// endUserSessions reads everything before it buffers anything: a buffered write is not
// read back, and the sessions to end are the ones that stand now.
func (m *Manager) endUserSessions(ctx context.Context, txn resource.ReadWriteTransaction, userID impulse.UUID) error {
	own, err := m.store.LiveSessions(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.LiveSessions()")
	}
	username, found, err := m.store.Username(ctx, txn, userID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Username()")
	}
	var held []impulse.UUID
	if found {
		records, err := m.store.ImpersonationsHeldBy(ctx, txn, username)
		if err != nil {
			return errors.Wrap(err, "identity.Store.ImpersonationsHeldBy()")
		}
		for _, record := range records {
			held = append(held, record.SessionID)
		}
	}

	// One plan per session, so a session that is the user's own and held by the user
	// as actor at once (the user viewing as themselves) is ended once.
	type ending struct {
		session bool
		record  bool
	}
	plan := make(map[impulse.UUID]*ending, len(own)+len(held))
	order := make([]impulse.UUID, 0, len(own)+len(held))
	for _, id := range own {
		live, _, err := m.store.Impersonation(ctx, txn, id)
		if err != nil {
			return errors.Wrap(err, "identity.Store.Impersonation()")
		}
		plan[id] = &ending{session: true, record: live}
		order = append(order, id)
	}
	for _, id := range held {
		if e, planned := plan[id]; planned {
			e.record = true

			continue
		}
		// The record outlives its session as evidence and names no session row it
		// can vouch for: the row is read, and ended only where it stands.
		expired, exists, err := m.store.SessionExpired(ctx, txn, id)
		if err != nil {
			return errors.Wrap(err, "identity.Store.SessionExpired()")
		}
		plan[id] = &ending{session: exists && !expired, record: true}
		order = append(order, id)
	}

	for _, id := range order {
		if plan[id].session {
			if err := m.store.EndSession(ctx, txn, id); err != nil {
				return errors.Wrap(err, "identity.Store.EndSession()")
			}
		}
		if plan[id].record {
			if err := m.store.EndImpersonation(ctx, txn, id, revoked); err != nil {
				return errors.Wrap(err, "identity.Store.EndImpersonation()")
			}
		}
	}

	return nil
}

// endSession ends one session where it stands, and its record where that stands. It
// answers whether the session exists.
func (m *Manager) endSession(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID) (found bool, err error) {
	live, _, err := m.store.Impersonation(ctx, txn, sessionID)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.Impersonation()")
	}

	return m.endSessionAndRecord(ctx, txn, sessionID, live)
}

// endSessionAndRecord ends the session where it stands and the record where
// recordLive says it does, reading the session before it buffers either write.
func (m *Manager) endSessionAndRecord(ctx context.Context, txn resource.ReadWriteTransaction, sessionID impulse.UUID, recordLive bool) (found bool, err error) {
	expired, found, err := m.store.SessionExpired(ctx, txn, sessionID)
	if err != nil {
		return false, errors.Wrap(err, "identity.Store.SessionExpired()")
	}
	if found && !expired {
		if err := m.store.EndSession(ctx, txn, sessionID); err != nil {
			return false, errors.Wrap(err, "identity.Store.EndSession()")
		}
	}
	if recordLive {
		if err := m.store.EndImpersonation(ctx, txn, sessionID, revoked); err != nil {
			return false, errors.Wrap(err, "identity.Store.EndImpersonation()")
		}
	}

	return found, nil
}
