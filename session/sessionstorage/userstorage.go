package sessionstorage

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/securehash"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/tracer"
)

// userStorage is the storage of a store that has users: the sessions, and the user
// operations over the users table. Auth builds on it.
type userStorage struct {
	sessionStorage
}

// CreateSession creates a new session for the request and returns its ID. When a custom
// session data configuration with a resolver is attached to the storage, the resolver
// runs inside the session-insert transaction; a resolver error aborts session creation.
// With no resolver it is a plain insert.
func (s *userStorage) CreateSession(ctx context.Context, req *sessioninfo.NewSessionRequest) (impulse.UUID, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	session := &dbtype.InsertSession{
		Username: req.Username,
	}

	id, err := s.db.InsertSession(ctx, session, req)
	if err != nil {
		return impulse.NilUUID, errors.Wrap(err, "db.InsertSession()")
	}

	return id, nil
}

// User returns the user record associated with the username
func (s *userStorage) User(ctx context.Context, id impulse.UUID) (*dbtype.SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	u, err := s.db.User(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "db.User()")
	}

	return u, nil
}

// UserByUserName returns the user record associated with the username
func (s *userStorage) UserByUserName(ctx context.Context, username string) (*dbtype.SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	u, err := s.db.UserByUserName(ctx, username)
	if err != nil {
		return nil, errors.Wrap(err, "db.UserByUserName()")
	}

	return u, nil
}

// CreateUser creates a new user. customData is nil or *U for the configured custom
// user data struct type; when non-nil it is written atomically with the user insert
// and requires a custom user data configuration on the storage.
func (s *userStorage) CreateUser(ctx context.Context, user *dbtype.InsertSessionUser, customData any) (*dbtype.SessionUser, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	u, err := s.db.CreateUser(ctx, user, customData)
	if err != nil {
		return nil, errors.Wrap(err, "db.CreateUser()")
	}

	return u, nil
}

// SetUserUsername updates the user username and, atomically, the Username
// on every active session row for that user.
func (s *userStorage) SetUserUsername(ctx context.Context, id impulse.UUID, newUsername string) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.SetUserUsername(ctx, id, newUsername); err != nil {
		return errors.Wrap(err, "db.SetUserUsername()")
	}

	return nil
}

// SetUserPasswordHash updates the user password hash
func (s *userStorage) SetUserPasswordHash(ctx context.Context, id impulse.UUID, hash *securehash.Hash) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.SetUserPasswordHash(ctx, id, hash); err != nil {
		return errors.Wrap(err, "db.SetUserPasswordHash()")
	}

	return nil
}

// DeactivateUser deactivates a user
func (s *userStorage) DeactivateUser(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.DeactivateUser(ctx, id); err != nil {
		return errors.Wrap(err, "db.DeactivateUser()")
	}

	return nil
}

// DeleteUser deletes a user
func (s *userStorage) DeleteUser(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.DeleteUser(ctx, id); err != nil {
		return errors.Wrap(err, "db.DeleteUser()")
	}

	return nil
}

// ActivateUser activates a user
func (s *userStorage) ActivateUser(ctx context.Context, id impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := s.db.ActivateUser(ctx, id); err != nil {
		return errors.Wrap(err, "db.ActivateUser()")
	}

	return nil
}
