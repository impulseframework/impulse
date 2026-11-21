// Package vouch holds the mark of the one sign-in method whose word that an address is
// verified the session library keeps (sessioninfo.Identity.EmailVerified): Google's.
//
// The people who run an identity provider can mark any address verified, so the library
// takes that word from one method alone, and knows the method by a mark it carries
// (signin.Method.AddressAuthority), never by its name: a name is the application's to
// give, and a method of the application's own may take any.
//
// The mark is a value with nothing exported in it, and the go command keeps this
// package to the session library's directory: no package outside that directory can
// import a package under its internal directory. So only the library can make a mark,
// or name its type, and the method an application builds carries the zero value, which
// is no mark.
//
// A value can still be copied by code that cannot name its type. An application that
// stood in for the Auth at the registration of Google's method (a signin.Core of its
// own) would be handed the method Google's package built, mark and all, and could carry
// the mark over to a method of its own. That is no method taking Google's name by
// mistake, and the type checker does not refuse it.
//
// The mark says which method is Google's, and not who answered for the token: a
// verifier the application sets in the Google method's configuration
// (google.Config.Authenticator) is trusted as Google's, and production leaves it nil.
package vouch

// Mark is the mark of a sign-in method whose word for an address the library keeps.
// Google's method carries Google's (Google), and no other method carries one: the zero
// value is no mark.
type Mark struct {
	google bool
}

// Google returns Google's mark. It is for the method the library's Google package
// builds (session/google) and for no other.
func Google() Mark {
	return Mark{google: true}
}

// IsGoogle reports whether m is Google's mark. The zero value is not.
func (m Mark) IsGoogle() bool {
	return m.google
}
