package session

import "github.com/impulseframework/impulse/session/internal/basesession"

var _ PreauthHandlers = &Preauth[NoCustomData]{}

// PreauthHandlers defines the interface for pre-authentication session handlers.
type PreauthHandlers interface {
	basesession.Handlers
}
