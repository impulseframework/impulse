package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/tracer"
)

// revokedByHand is the reason a token carries when a person revoked it.
const revokedByHand = string(sessionstorage.TokenRevokedByHand)

// RevokeToken buffers the revocation of one bearer token, in the caller's transaction:
// once it commits, the next request that carries the token is refused, since the
// session library reads the row on every request. A token is revoked by a mark and its
// row stays. A token that belongs to a device authorization takes its family with it,
// every token of the authorization that is not revoked yet, the expired ones among
// them, so no successor of it stays in use. A token that is revoked or expired already
// is refused as one that does not exist: there is nothing live to revoke.
//
// Shared rule: the session library's RevokeToken, here for any person's token, where
// the library's own route revokes the caller's.
func (m *Manager) RevokeToken(ctx context.Context, txn resource.ReadWriteTransaction, tokenID impulse.UUID) error {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	token, found, err := m.store.Token(ctx, txn, tokenID)
	if err != nil {
		return errors.Wrap(err, "identity.Store.Token()")
	}
	if !found || token.Revoked || !token.ExpiresAt.After(m.now()) {
		return httpio.NewNotFoundMessage("no live token")
	}
	targets := []impulse.UUID{tokenID}
	if token.Family != nil {
		if targets, err = m.store.FamilyTokens(ctx, txn, *token.Family); err != nil {
			return errors.Wrap(err, "identity.Store.FamilyTokens()")
		}
	}
	for _, id := range targets {
		if err := m.store.RevokeToken(ctx, txn, id, revokedByHand); err != nil {
			return errors.Wrap(err, "identity.Store.RevokeToken()")
		}
	}

	return nil
}
