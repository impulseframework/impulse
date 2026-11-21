package session

import (
	"github.com/impulseframework/impulse/session/internal/mockstore"
	"github.com/impulseframework/impulse/session/internal/testkey"
	gomock "go.uber.org/mock/gomock"
)

const cookieKey = testkey.CookieKey

// The session-type constructors probe the storage for misconfiguration (the custom
// user data type link). These helpers return store mocks with the probe defaulted to
// "not configured" so tests exercising other
// behavior construct session types without boilerplate; tests targeting the probes
// build raw mocks with explicit expectations instead.

func newPreauthStoreMock(ctrl *gomock.Controller) *mockstore.PreauthStore {
	storage := mockstore.NewPreauthStore(ctrl)
	storage.EXPECT().CustomUserDataType().Return(nil).AnyTimes()

	return storage
}
