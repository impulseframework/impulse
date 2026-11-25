// Package actor holds the person who made a change, named by their account where no
// session carries them, as the library's own packages hand one to who made a change
// (resource.AccountProcessEvent). It is internal so that only the library can make one:
// an application names who made a change from a request's session or by a process's
// name, never by texts it holds.
package actor

import "github.com/impulseframework/impulse"

// Account is a person by the id of their account and their username on the day. Its
// fields are unexported, so no struct another package spells converts to it: a value is
// made by NewAccount alone.
type Account struct {
	userID   impulse.UUID
	username string
}

// NewAccount answers the person by the id of their account and their username.
func NewAccount(userID impulse.UUID, username string) Account {
	return Account{userID: userID, username: username}
}

// UserID is the id of the person's account.
func (a Account) UserID() impulse.UUID {
	return a.userID
}

// Username is the person's username on the day.
func (a Account) Username() string {
	return a.username
}
