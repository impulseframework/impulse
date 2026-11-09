package session

import (
	"github.com/impulseframework/impulse/session/internal/testkey"
	"github.com/impulseframework/impulse/session/sessionstorage/mock/mock_sessionstorage"
	gomock "go.uber.org/mock/gomock"
)

const cookieKey = testkey.CookieKey

// The session-type constructors probe the storage for misconfiguration (the custom
// user data type link). These helpers return store mocks with the probe defaulted to
// "not configured" so tests exercising other
// behavior construct session types without boilerplate; tests targeting the probes
// build raw mocks with explicit expectations instead.

func newPreauthStoreMock(ctrl *gomock.Controller) *mock_sessionstorage.MockPreauthStore {
	storage := mock_sessionstorage.NewMockPreauthStore(ctrl)
	storage.EXPECT().CustomUserDataType().Return(nil).AnyTimes()

	return storage
}
