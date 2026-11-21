// Package mockstore holds the mocks of a session store that the session library's own
// tests run over. The mocks an application gets (mock_sessionstorage) refuse the write
// of an impersonated session and hold none of the reads that decide one, because nothing
// outside the session library reaches them. The library's tests have to see the library
// make the write and take what the reads answer, so the mocks here take them as
// expectations: each is the generated mock of the store's methods with a write built by
// the library's internal package (sealed.NewImpersonatedSessionWrite) over a mock of its
// own, Sealed, and with the reads built over the mock's own Session, User and
// UserByUserName, as a store the library made has them.
//
// The package is internal, so no application can import it: a store whose write and
// reads run code of a caller's choosing is the library's alone to build.
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
// session is expected on Sealed, and whose read of a session for the library is the
// mock's own Session.
type BaseStore struct {
	*mock_sessionstorage.MockBaseStoreMethods
	sealed.ImpersonatedSessionWrite
	sealed.SessionRead
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewBaseStore creates a mock of sessionstorage.BaseStore.
func NewBaseStore(ctrl *gomock.Controller) *BaseStore {
	write := NewMockSealedWrite(ctrl)

	store := &BaseStore{
		MockBaseStoreMethods:     mock_sessionstorage.NewMockBaseStoreMethods(ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
	store.SessionRead = sealed.NewSessionRead(store.Session, store)

	return store
}

// PreauthStore is a mock of sessionstorage.PreauthStore whose write of an impersonated
// session is expected on Sealed, and whose read of a session for the library is the
// mock's own Session.
type PreauthStore struct {
	*mock_sessionstorage.MockPreauthStoreMethods
	sealed.ImpersonatedSessionWrite
	sealed.SessionRead
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewPreauthStore creates a mock of sessionstorage.PreauthStore.
func NewPreauthStore(ctrl *gomock.Controller) *PreauthStore {
	write := NewMockSealedWrite(ctrl)

	store := &PreauthStore{
		MockPreauthStoreMethods:  mock_sessionstorage.NewMockPreauthStoreMethods(ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
	store.SessionRead = sealed.NewSessionRead(store.Session, store)

	return store
}

// AuthStore is a mock of sessionstorage.AuthStore whose write of an impersonated
// session is expected on Sealed, and whose reads of a session and of an account for the
// library are the mock's own Session, User and UserByUserName.
type AuthStore[Tx any] struct {
	*mock_sessionstorage.MockAuthStoreMethods[Tx]
	sealed.ImpersonatedSessionWrite
	sealed.SessionRead
	sealed.UserRead
	// Sealed takes the expectations of the write of an impersonated session:
	// Sealed.EXPECT().CreateImpersonatedSession(ctx, req, imp).
	Sealed *MockSealedWrite
}

// NewAuthStore creates a mock of sessionstorage.AuthStore.
func NewAuthStore[Tx any](ctrl *gomock.Controller) *AuthStore[Tx] {
	write := NewMockSealedWrite(ctrl)

	store := &AuthStore[Tx]{
		MockAuthStoreMethods:     mock_sessionstorage.NewMockAuthStoreMethods[Tx](ctrl),
		ImpersonatedSessionWrite: sealed.NewImpersonatedSessionWrite(write.CreateImpersonatedSession),
		Sealed:                   write,
	}
	store.SessionRead = sealed.NewSessionRead(store.Session, store)
	store.UserRead = sealed.NewUserRead(store.User, store.UserByUserName)

	return store
}

var (
	_ sessionstorage.BaseStore         = (*BaseStore)(nil)
	_ sessionstorage.PreauthStore      = (*PreauthStore)(nil)
	_ sessionstorage.AuthStore[string] = (*AuthStore[string])(nil)
)
