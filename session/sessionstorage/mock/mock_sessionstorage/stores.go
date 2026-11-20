package mock_sessionstorage

import (
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// The mocks of a session store are written here, around the generated mocks of the
// store's methods (MockBaseStoreMethods, MockPreauthStoreMethods and
// MockAuthStoreMethods). A store has one method the generated mocks cannot give it: the
// write of an impersonated session, whose name belongs to the session library's
// internal package, so that no package outside the library's directory can call it or
// declare it. Each mock here has it by embedding sessionstorage.NoImpersonatedSessions,
// whose write refuses with sessionstorage.ErrImpersonationSealed and writes nothing: a
// session over a mocked store starts no impersonated session, and no expectation can be
// set on that write. A test of an impersonated session starting runs over a store the
// library made.
//
// The generated mocks carry a method named createImpersonatedSession of their own, from
// the interface's source. It belongs to this package, it is not the store's write, and
// nothing calls it.

// MockBaseStore is a mock of the BaseStore interface.
type MockBaseStore struct {
	*MockBaseStoreMethods
	sessionstorage.NoImpersonatedSessions
}

// NewMockBaseStore creates a new mock instance.
func NewMockBaseStore(ctrl *gomock.Controller) *MockBaseStore {
	return &MockBaseStore{MockBaseStoreMethods: NewMockBaseStoreMethods(ctrl)}
}

// MockPreauthStore is a mock of the PreauthStore interface.
type MockPreauthStore struct {
	*MockPreauthStoreMethods
	sessionstorage.NoImpersonatedSessions
}

// NewMockPreauthStore creates a new mock instance.
func NewMockPreauthStore(ctrl *gomock.Controller) *MockPreauthStore {
	return &MockPreauthStore{MockPreauthStoreMethods: NewMockPreauthStoreMethods(ctrl)}
}

// MockAuthStore is a mock of the AuthStore interface.
type MockAuthStore[Tx any] struct {
	*MockAuthStoreMethods[Tx]
	sessionstorage.NoImpersonatedSessions
}

// NewMockAuthStore creates a new mock instance.
func NewMockAuthStore[Tx any](ctrl *gomock.Controller) *MockAuthStore[Tx] {
	return &MockAuthStore[Tx]{MockAuthStoreMethods: NewMockAuthStoreMethods[Tx](ctrl)}
}

var (
	_ sessionstorage.BaseStore         = (*MockBaseStore)(nil)
	_ sessionstorage.PreauthStore      = (*MockPreauthStore)(nil)
	_ sessionstorage.AuthStore[string] = (*MockAuthStore[string])(nil)
)
