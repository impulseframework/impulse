package signin

import (
	"strings"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The two connections that stand for everyone who is in no organization. An identity
// that came through one of them was vouched for by the provider alone, so the
// connection names no organization: every such account in the world carries the same
// value. Neither is a directory a tenant can take as its own (Directory.Organization).
const (
	// GoogleAnyAccountConnection is the connection of a Google account that is in none
	// of a registration's named Workspaces and was admitted as any Google account: the
	// provider itself (google.AnyAccountConnection).
	GoogleAnyAccountConnection = "accounts.google.com"
	// MicrosoftPersonalAccountsConnection is the connection of a Microsoft personal
	// account (outlook.com, hotmail.com, Xbox): the one tenant id every personal
	// account's token carries (azure.ConsumerTenantID).
	MicrosoftPersonalAccountsConnection = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

// PersonalAccounts reports whether the directory is one of the two values that stand
// for everyone who is in no organization: any Google account, or Microsoft's personal
// accounts. Such a value is no organization's directory, so no tenant takes it as its
// own, and a person who signed in through it belongs to no tenant's directory.
func (d Directory) PersonalAccounts() bool {
	switch d.Method {
	case sessioninfo.MethodGoogle:
		return d.Connection == GoogleAnyAccountConnection
	case sessioninfo.MethodAzure:
		return strings.EqualFold(d.Connection, MicrosoftPersonalAccountsConnection)
	default:
		return false
	}
}

// Organization reports whether the directory is an organization's: a sign-in method
// that signs a person in through a directory (sessioninfo.AuthMethod.ThroughDirectory)
// with a connection that names one organization, so neither of the two values that
// stand for everyone who is in no organization (PersonalAccounts). It is what a tenant
// can take as its directory, and what a tenant's declaration admits or refuses a
// session by: a password, a session the application started itself, an impersonation
// and a personal account are no organization's directory.
func (d Directory) Organization() bool {
	return d.Method.ThroughDirectory() && d.Connection != "" && !d.PersonalAccounts()
}
