package spanner

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/tracer"
	"google.golang.org/api/iterator"
)

// mailLinkColumns are the mail links table's columns.
const mailLinkColumns = `Id, TokenHash, Kind, Email, UserId, TenantId, RoleNames, InvitedByUserId, CreatedAt, ExpiresAt, ConsumedAt, Name, NewTenant, EveryTenant`

// mailLinkRow is a mail link as the table holds it.
type mailLinkRow struct {
	ID              string             `spanner:"Id"`
	TokenHash       []byte             `spanner:"TokenHash"`
	Kind            string             `spanner:"Kind"`
	Email           string             `spanner:"Email"`
	UserID          spanner.NullString `spanner:"UserId"`
	TenantID        spanner.NullString `spanner:"TenantId"`
	RoleNames       []string           `spanner:"RoleNames"`
	InvitedByUserID spanner.NullString `spanner:"InvitedByUserId"`
	CreatedAt       time.Time          `spanner:"CreatedAt"`
	ExpiresAt       time.Time          `spanner:"ExpiresAt"`
	ConsumedAt      spanner.NullTime   `spanner:"ConsumedAt"`
	Name            spanner.NullString `spanner:"Name"`
	NewTenant       spanner.NullString `spanner:"NewTenant"`
	EveryTenant     bool               `spanner:"EveryTenant"`
}

// newMailLinkRow renders l as the table holds it: created at the commit time and not
// consumed, whatever l carries.
func newMailLinkRow(l *dbtype.MailLink) (*mailLinkRow, error) {
	r := &mailLinkRow{
		ID: l.ID.String(), TokenHash: l.TokenHash, Kind: string(l.Kind), Email: l.Email, RoleNames: l.RoleNames,
		CreatedAt: spanner.CommitTimestamp, ExpiresAt: l.ExpiresAt, EveryTenant: l.EveryTenant,
	}
	if l.UserID.Valid {
		r.UserID = spanner.NullString{StringVal: l.UserID.String(), Valid: true}
	}
	if l.TenantID != "" {
		r.TenantID = spanner.NullString{StringVal: l.TenantID, Valid: true}
	}
	if l.InvitedByUserID.Valid {
		r.InvitedByUserID = spanner.NullString{StringVal: l.InvitedByUserID.String(), Valid: true}
	}
	if l.Name != "" {
		r.Name = spanner.NullString{StringVal: l.Name, Valid: true}
	}
	newTenant, err := dbtype.NewTenantColumn(l.NewTenant)
	if err != nil {
		return nil, errors.Wrap(err, "dbtype.NewTenantColumn()")
	}
	if newTenant != nil {
		r.NewTenant = spanner.NullString{StringVal: *newTenant, Valid: true}
	}

	return r, nil
}

func (r *mailLinkRow) link() (*dbtype.MailLink, error) {
	id, err := impulse.UUIDFromString(r.ID)
	if err != nil {
		return nil, errors.Wrap(err, "impulse.UUIDFromString()")
	}
	l := &dbtype.MailLink{
		ID: id, TokenHash: r.TokenHash, Kind: dbtype.MailLinkKind(r.Kind), Email: r.Email, RoleNames: r.RoleNames,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EveryTenant: r.EveryTenant,
	}
	if r.UserID.Valid {
		userID, err := impulse.UUIDFromString(r.UserID.StringVal)
		if err != nil {
			return nil, errors.Wrap(err, "impulse.UUIDFromString()")
		}
		l.UserID = impulse.NullUUIDFromUUID(userID)
	}
	if r.TenantID.Valid {
		l.TenantID = r.TenantID.StringVal
	}
	if r.Name.Valid {
		l.Name = r.Name.StringVal
	}
	if r.InvitedByUserID.Valid {
		invitedBy, err := impulse.UUIDFromString(r.InvitedByUserID.StringVal)
		if err != nil {
			return nil, errors.Wrap(err, "impulse.UUIDFromString()")
		}
		l.InvitedByUserID = impulse.NullUUIDFromUUID(invitedBy)
	}
	if r.ConsumedAt.Valid {
		consumedAt := r.ConsumedAt.Time
		l.ConsumedAt = &consumedAt
	}
	if r.NewTenant.Valid {
		newTenant, err := dbtype.NewTenantFromColumn(&r.NewTenant.StringVal)
		if err != nil {
			return nil, errors.Wrap(err, "dbtype.NewTenantFromColumn()")
		}
		l.NewTenant = newTenant
	}

	return l, nil
}

// InsertMailLink inserts l.
func (s *SessionStorageDriver) InsertMailLink(ctx context.Context, l *dbtype.MailLink) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	if err := l.Kind.Validate(); err != nil {
		return errors.Wrap(err, "dbtype.MailLinkKind.Validate()")
	}
	row, err := newMailLinkRow(l)
	if err != nil {
		return err
	}
	m, err := spanner.InsertStruct(s.mailLinksTableName, row)
	if err != nil {
		return errors.Wrap(err, "spanner.InsertStruct()")
	}
	if _, err := s.spanner.Apply(ctx, []*spanner.Mutation{m}); err != nil {
		return errors.Wrap(err, "spanner.Client.Apply()")
	}

	return nil
}

// queryMailLinks runs stmt and renders its rows.
func (s *SessionStorageDriver) queryMailLinks(ctx context.Context, stmt spanner.Statement) ([]*dbtype.MailLink, error) {
	iter := s.spanner.Single().Query(ctx, stmt)
	defer iter.Stop()

	var links []*dbtype.MailLink
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return links, nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "spanner.RowIterator.Next()")
		}
		var r mailLinkRow
		if err := row.ToStruct(&r); err != nil {
			return nil, errors.Wrap(err, "spanner.Row.ToStruct()")
		}
		l, err := r.link()
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
}

// MailLinkByToken returns the mail link holding tokenHash that is live at now: not
// consumed and not expired. Any other is NotFound.
func (s *SessionStorageDriver) MailLinkByToken(ctx context.Context, tokenHash []byte, now time.Time) (*dbtype.MailLink, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT %s FROM %s WHERE TokenHash = @tokenHash AND ConsumedAt IS NULL AND ExpiresAt > @now`, mailLinkColumns, s.mailLinksTableName))
	stmt.Params["tokenHash"] = tokenHash
	stmt.Params["now"] = now

	links, err := s.queryMailLinks(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, httpio.NewNotFoundMessage("no live mail link")
	}

	return links[0], nil
}

// LiveMailLinks lists the links of kind that are live at now, oldest first.
func (s *SessionStorageDriver) LiveMailLinks(ctx context.Context, kind dbtype.MailLinkKind, now time.Time) ([]*dbtype.MailLink, error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	stmt := spanner.NewStatement(fmt.Sprintf(`SELECT %s FROM %s WHERE Kind = @kind AND ConsumedAt IS NULL AND ExpiresAt > @now ORDER BY CreatedAt, Id`, mailLinkColumns, s.mailLinksTableName))
	stmt.Params["kind"] = string(kind)
	stmt.Params["now"] = now

	return s.queryMailLinks(ctx, stmt)
}

// errMailLinkNotLive is the transaction's signal that the conditional update changed no
// row.
var errMailLinkNotLive = errors.New("no live mail link")

// ConsumeMailLink marks the mail link holding tokenHash consumed, at the commit time,
// when it is live at now. A link that is not live, because it was consumed, has
// expired or does not exist, is NotFound: the update is conditional, so a link is
// consumed once.
func (s *SessionStorageDriver) ConsumeMailLink(ctx context.Context, tokenHash []byte, now time.Time) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return s.consumeMailLink(ctx, "TokenHash", tokenHash, now)
}

// RevokeMailLink marks the live mail link id consumed, as ConsumeMailLink does by the
// token: an invitation withdrawn by hand. A link that is not live is NotFound.
func (s *SessionStorageDriver) RevokeMailLink(ctx context.Context, id impulse.UUID, now time.Time) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	return s.consumeMailLink(ctx, idColumn, id.String(), now)
}

// FailMailLink marks the live mail link id consumed and as one whose message could not
// be mailed, both at the commit time: the link ends where its mail failed, and its row
// says why. A link that is not live is NotFound.
func (s *SessionStorageDriver) FailMailLink(ctx context.Context, id impulse.UUID, now time.Time) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	_, err := s.spanner.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		return s.endMailLinkIn(ctx, txn, mailLinkMailFailed, idColumn, id.String(), now)
	})
	if errors.Is(err, errMailLinkNotLive) {
		return httpio.NewNotFoundMessage("no live mail link")
	}
	if err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

// consumeMailLink consumes the live link whose column holds value, in a transaction of
// its own.
func (s *SessionStorageDriver) consumeMailLink(ctx context.Context, column string, value any, now time.Time) error {
	_, err := s.spanner.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		return s.consumeMailLinkIn(ctx, txn, column, value, now)
	})
	if errors.Is(err, errMailLinkNotLive) {
		return httpio.NewNotFoundMessage("no live mail link")
	}
	if err != nil {
		return errors.Wrap(err, "spanner.Client.ReadWriteTransaction()")
	}

	return nil
}

// ConsumeMailLinkIn consumes the live mail link id inside tx, at the commit time. A link
// that is not live is NotFound, and the transaction may go on.
func (s *SessionStorageDriver) ConsumeMailLinkIn(ctx context.Context, tx *spanner.ReadWriteTransaction, id impulse.UUID, now time.Time) error {
	if err := s.consumeMailLinkIn(ctx, tx, idColumn, id.String(), now); err != nil {
		if errors.Is(err, errMailLinkNotLive) {
			return httpio.NewNotFoundMessage("no live mail link")
		}

		return err
	}

	return nil
}

// The columns the end of a mail link sets, each to the commit time: a link that was
// followed or withdrawn is consumed, and one whose message could not be mailed is
// consumed and says the mail failed.
const (
	mailLinkConsumed   = "ConsumedAt = PENDING_COMMIT_TIMESTAMP()"
	mailLinkMailFailed = "ConsumedAt = PENDING_COMMIT_TIMESTAMP(), MailFailedAt = PENDING_COMMIT_TIMESTAMP()"
)

func (s *SessionStorageDriver) consumeMailLinkIn(ctx context.Context, txn *spanner.ReadWriteTransaction, column string, value any, now time.Time) error {
	return s.endMailLinkIn(ctx, txn, mailLinkConsumed, column, value, now)
}

// endMailLinkIn ends the live mail link whose column holds value inside txn, by a
// conditional update that sets the columns set names; errMailLinkNotLive when no link
// is live there.
func (s *SessionStorageDriver) endMailLinkIn(ctx context.Context, txn *spanner.ReadWriteTransaction, set, column string, value any, now time.Time) error {
	stmt := spanner.NewStatement(fmt.Sprintf(`UPDATE %s SET %s WHERE %s = @value AND ConsumedAt IS NULL AND ExpiresAt > @now`, s.mailLinksTableName, set, column))
	stmt.Params["value"] = value
	stmt.Params["now"] = now

	n, err := txn.Update(ctx, stmt)
	if err != nil {
		return errors.Wrap(err, "spanner.ReadWriteTransaction.Update()")
	}
	if n == 0 {
		return errMailLinkNotLive
	}

	return nil
}
