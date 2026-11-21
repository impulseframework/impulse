package mock_sessionstorage

import (
	"github.com/impulseframework/impulse/session/sessionstorage"
	"go.uber.org/mock/gomock"
)

// The mocks of a session store are written here, around the generated mocks of the
// store's methods (MockBaseStoreMethods, MockPreauthStoreMethods, MockUserStoreMethods
// and MockAuthStoreMethods). A store has methods the generated mocks cannot give it:
// the write of an impersonated session, and the reads that decide one (the actor's own
// session, the account acted as, and the session of every later request). Their names
// belong to the session library's internal package, so that no package outside the
// library's directory can call them or declare them. Each mock here has them by
// embedding sessionstorage.NoImpersonatedSessions, whose write refuses with
// sessionstorage.ErrImpersonationSealed and writes nothing, and whose reads answer the
// same error: a session over a mocked store starts no impersonated session, no
// expectation can be set on that write, and a Session expectation that answers a session
// with an impersonation record is refused as an invalid session. A test of an
// impersonated session runs over a store the library made, or puts the session it wants
// in the request's context itself.
//
// The generated mocks carry methods named createImpersonatedSession, readSession,
// sessionReadOwner, readUser and readUserByUserName of their own, from the interfaces'
// source. They belong to this package, they are not the store's, and nothing calls them.

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

// MockUserStore is a mock of the UserStore interface.
type MockUserStore struct {
	*MockUserStoreMethods
	sessionstorage.NoImpersonatedSessions
}

// NewMockUserStore creates a new mock instance.
func NewMockUserStore(ctrl *gomock.Controller) *MockUserStore {
	return &MockUserStore{MockUserStoreMethods: NewMockUserStoreMethods(ctrl)}
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
	_ sessionstorage.UserStore         = (*MockUserStore)(nil)
	_ sessionstorage.AuthStore[string] = (*MockAuthStore[string])(nil)
)
