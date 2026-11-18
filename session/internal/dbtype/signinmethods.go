package dbtype

import "time"

// SignInMethod is a row of the sign-in methods table: what an administrator chose, in
// the running application, for one sign-in method of the Auth, named by the name the
// method is registered under ("password", or the application's name for an external
// registration). OpenSignUp says the method admits a person who holds no account and no
// invitation; OpenedFor is whom the method admitted when it was opened, as the
// registration's audience spells itself (signin.Audience.Key), nil for a row that opens
// nothing; DefaultRole names the role such a person is given with their account, as
// the application's hook understands it, and holds nothing in an application whose
// people get their roles in a tenant. A method with no row is invite only, which is
// what a row with OpenSignUp false says too. UpdatedAt is the database's time at the
// write.
type SignInMethod struct {
	Name        string
	OpenSignUp  bool
	OpenedFor   *string
	DefaultRole *string
	UpdatedAt   time.Time
}

// OpenFor reports whether the row opens its method to a person with no account and no
// invitation, for a method whose registration admits audience now (signin.Audience.Key).
// A row is honored for the audience it was opened for and no other: the administrator
// who opened the method confirmed a sentence that named whom it admits, and a
// registration that code has since widened, or changed at all, is invite only until it
// is opened again. No row is invite only, so a nil row is not open, and neither is a
// row that does not say whom it was opened for.
func (m *SignInMethod) OpenFor(audience string) bool {
	return m != nil && m.OpenSignUp && m.OpenedFor != nil && *m.OpenedFor == audience
}

// Role is the role the row gives a person who signs up through its method, "" for
// none and for no row.
func (m *SignInMethod) Role() string {
	if m == nil || m.DefaultRole == nil {
		return ""
	}

	return *m.DefaultRole
}
