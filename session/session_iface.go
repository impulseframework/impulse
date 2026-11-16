// Package session provides cookie-backed sessions for web applications.
//
// Auth is the session: one cookie, one session table and one middleware chain that
// any registered sign-in method can establish. The password method lives here
// (PasswordSignIn); the redirect-based methods are their own packages, built on the
// signin contract: azure (Microsoft Entra ID), google (Google Workspace) and workos
// (WorkOS SSO). Preauth lets an application bring its own authentication and still use
// the session handlers.
//
// Every session type supports custom session data: an app-defined table whose row is
// resolved atomically inside the session-insert transaction (or supplied per call),
// decoded on every authenticated request, reset on session regeneration, and cleaned up
// with the session via the table's ON DELETE CASCADE. See the "Custom session data"
// section of the README for the full lifecycle, schema contract, and failure semantics.
//
// Auth additionally supports custom user data: an app-defined table keyed by the
// durable user record (SessionUsers), written atomically with user creation and read
// on demand — it survives logout, expiry, and regeneration, and dies with the user.
// See the "Custom user data" section of the README.
package session

import (
	"github.com/impulseframework/impulse/session/internal/basesession"
)

// LogHandler defines the handler signature required for handling logs.
type LogHandler = basesession.LogHandler
