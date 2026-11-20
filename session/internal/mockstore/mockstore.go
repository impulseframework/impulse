// Package mockstore holds the mocks of a session store that the session library's own
// tests run over. The mocks an application gets (mock_sessionstorage) refuse the write
// of an impersonated session, because nothing outside the session library reaches that
// write. The library's tests have to see the library make it, so the mocks here take it
// as an expectation: each is the generated mock of the store's methods with a write
// built by the library's internal package (sealed.NewImpersonatedSessionWrite) over a
// mock of its own, Sealed.
//
// The package is internal, so no application can import it: a store whose write runs
// code of a caller's choosing is the library's alone to build.
package mockstore

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/sealed"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	"go.uber.org/mock/gomock"
)

// SealedWrite is the write of an impersonated session as a test expects it: the call
// the session library makes on the store through its internal package, under a name a
// test can write.
type SealedWrite interface {
	// CreateImpersonatedSession stands for the store's write of an impersonated session
	// for the request, with its record.
	CreateImpersonatedSession(ctx context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error)
}

// BaseStore is a mock of sessionstorage.BaseStore whose write of an impersonated
// session is expected on Sealed.
type BaseStore struct {
	*mock_sessionstorage.MockBaseStoreMethods
	sealed.ImpersonatedSessionWrite
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewBaseStore creates a mock of sessionstorage.BaseStore.
func NewBaseStore(ctrl *gomock.Controller) *BaseStore {
	write := NewMockSealedWrite(ctrl)

	return &BaseStore{
		MockBaseStoreMethods:     mock_sessionstorage.NewMockBaseStoreMethods(ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
}

// PreauthStore is a mock of sessionstorage.PreauthStore whose write of an impersonated
// session is expected on Sealed.
type PreauthStore struct {
	*mock_sessionstorage.MockPreauthStoreMethods
	sealed.ImpersonatedSessionWrite
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewPreauthStore creates a mock of sessionstorage.PreauthStore.
func NewPreauthStore(ctrl *gomock.Controller) *PreauthStore {
	write := NewMockSealedWrite(ctrl)

	return &PreauthStore{
		MockPreauthStoreMethods:  mock_sessionstorage.NewMockPreauthStoreMethods(ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
}

// AuthStore is a mock of sessionstorage.AuthStore whose write of an impersonated
// session is expected on Sealed.
type AuthStore[Tx any] struct {
	*mock_sessionstorage.MockAuthStoreMethods[Tx]
	sealed.ImpersonatedSessionWrite
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewAuthStore creates a mock of sessionstorage.AuthStore.
func NewAuthStore[Tx any](ctrl *gomock.Controller) *AuthStore[Tx] {
	write := NewMockSealedWrite(ctrl)

	return &AuthStore[Tx]{
		MockAuthStoreMethods:     mock_sessionstorage.NewMockAuthStoreMethods[Tx](ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
}

var (
	_ sessionstorage.BaseStore         = (*BaseStore)(nil)
	_ sessionstorage.PreauthStore      = (*PreauthStore)(nil)
	_ sessionstorage.AuthStore[string] = (*AuthStore[string])(nil)
)
