package drivertest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// Expiring names one row of each table whose rows are removed after they expire, and the
// sign-in code under the pending sign-in.
type Expiring struct {
	Token, TrustedDevice, DeviceCode, PendingSignIn, FactorCode impulse.UUID
}

// InsertExpiring inserts through d one row of each table whose rows are removed after
// they expire, each expiring at expiresAt: an application token and a trusted device of
// userID, a device code nobody decided, and a pending sign-in of userID with one sign-in
// code under it. n keeps the hashes and the user code of one call apart from another
// call's. On PostgreSQL an insert removes the rows of its table that are past their
// period, so a row inserted already past it is gone when this returns: stage such a
// row's age with SQL after a live insert.
func InsertExpiring(ctx context.Context, t *testing.T, d AuthDriver, userID impulse.UUID, n byte, expiresAt time.Time) Expiring {
	t.Helper()

	token := newToken(dbtype.TokenApplication, userID, tokenHash(n))
	token.ExpiresAt = expiresAt
	if err := d.InsertToken(ctx, token); err != nil {
		t.Fatalf("InsertToken(%d) error = %v", n, err)
	}

	device := newTrustedDevice(userID, tokenHash(n))
	device.ExpiresAt = expiresAt
	if err := d.InsertTrustedDevice(ctx, device); err != nil {
		t.Fatalf("InsertTrustedDevice(%d) error = %v", n, err)
	}

	code := newDeviceCode(fmt.Sprintf("XPRNG%03d", n), tokenHash(n))
	code.ExpiresAt = expiresAt
	if err := d.InsertDeviceCode(ctx, code); err != nil {
		t.Fatalf("InsertDeviceCode(%d) error = %v", n, err)
	}

	pending := newPendingSignIn(impulse.NullUUIDFromUUID(userID), tokenHash(n))
	pending.ExpiresAt = expiresAt
	if err := d.InsertPendingSignIn(ctx, pending); err != nil {
		t.Fatalf("InsertPendingSignIn(%d) error = %v", n, err)
	}

	factor := newFactorCode(pending.ID, tokenHash(n))
	factor.ExpiresAt = expiresAt
	if err := d.InsertFactorCode(ctx, factor, time.Now()); err != nil {
		t.Fatalf("InsertFactorCode(%d) error = %v", n, err)
	}

	return Expiring{Token: token.ID, TrustedDevice: device.ID, DeviceCode: code.ID, PendingSignIn: pending.ID, FactorCode: factor.ID}
}
