// Package sealed holds what the session library alone may hold: the key to the storage
// beneath an impersonated session. Go lets no module but the session library import a
// package under its internal directory, so an application can neither name the key's
// type nor make a key, and a call that needs one does not compile there.
package sealed

// ImpersonationKey is the key to the storage's write of an impersonated session and its
// record (sessionstorage.BaseStore.CreateImpersonatedSession). The session library makes
// one where it starts an impersonated session, from a request the application's
// declaration made (session.Impersonations), and nowhere else. The storage writes for
// no other caller: that write takes whatever principal, mask and actor it is handed,
// so a caller who reached it would go around the declaration, the grant it checked and
// the mask it gave.
//
// The key is a struct with a field of this package's, so no value an application can
// write stands in for it: not nil, not a constant, and not a struct literal of its
// own. A zero key, which reflection or a type parameter can still produce without
// naming the type, is not held, and the storage refuses it.
type ImpersonationKey struct {
	held bool
}

// NewImpersonationKey returns a key the storage accepts. It is called where the session
// library starts an impersonated session, and nowhere else.
func NewImpersonationKey() ImpersonationKey {
	return ImpersonationKey{held: true}
}

// Held reports whether the session library made the key: true for a key of
// NewImpersonationKey, false for the zero key.
func (k ImpersonationKey) Held() bool {
	return k.held
}
