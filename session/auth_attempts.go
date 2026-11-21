package session

import (
	"net"
	"net/http"
	"strings"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/session/internal/attempts"
)

// SignInAttemptName returns the name a username's failed sign-ins are counted under:
// the key of its row in the sign-in attempts table (Tables.SignInAttempts), the
// username in Unicode NFC and case-folded, as the users table normalizes usernames. A
// caller that clears a name's count in a transaction of its own (the generated user
// management) deletes the row of this key; a name with no row has no count.
func SignInAttemptName(username string) string {
	return attempts.Normalize(username)
}

// SignInAttemptKey returns the key of the row of the sign-in attempts table a listed
// name names. The table keeps the library's own counts beside the names people sign in
// under: the mail an address is sent, its sign-in codes, the codes sent to accept one
// invitation, its password reset links, and the guesses at the code of an account's
// first password, each under a key no username normalizes to. Such
// a key is the row's as it is listed; any other name is a username's
// (SignInAttemptName). A caller that clears the row a list shows, as the generated user
// management's unlock does, clears the row of this key.
func SignInAttemptKey(listed string) string {
	if attempts.IsOwnKey(listed) {
		return listed
	}

	return attempts.Normalize(listed)
}

// AccountAttemptKeys returns the key of every row the sign-in attempts table may keep
// for one account: the failed sign-ins counted under its username, the mail and the
// password reset links its address is sent, and the guesses at the code of its first
// password. email is the account's address, empty for an account that has none. A
// caller that asks whether a listed row is an account's compares the row's key
// (SignInAttemptKey) with these, as the generated user management does for its rule
// that nobody unlocks themselves while they act as someone else.
func AccountAttemptKeys(userID impulse.UUID, username, email string) []string {
	keys := []string{SignInAttemptName(username), firstPasswordKey(userID)}
	if strings.TrimSpace(email) != "" {
		keys = append(keys, mailLimitKey(email), resetLimitKey(email))
	}

	return keys
}

// clientOf identifies the request's client for the attempt limiter: its address and
// the login's XSRF cookie token, the cookie the XSRF guard reads, which a POST that
// reaches the handler carries. The token is a key, hashed by the limiter; nothing is
// written to the cookie.
func (a *Auth[S, U, Tx]) clientOf(r *http.Request) attempts.Client {
	client := attempts.Client{Address: clientAddress(r)}
	if cookie, err := r.Cookie(a.cookies.XSRFCookieName); err == nil {
		client.Cookie = cookie.Value
	}

	return client
}

// The headers a proxy reports the client's address in.
const (
	trueClientIPHeader = "True-Client-IP"
	realIPHeader       = "X-Real-IP"
	forwardedForHeader = "X-Forwarded-For"
)

// clientAddress is the client's address as the attempt limiter keys it: the address a
// proxy reports in True-Client-IP, X-Real-IP or the first entry of X-Forwarded-For,
// read in that order as chi's RealIP does, else the connection's remote address. The
// address key is a throttle, not a security boundary: the headers are the client's to
// forge, so a forged one picks another key and evades this key alone, while the name's
// lock and the edge's own throttle still hold the client; and behind a proxy that sets
// none of them, every client shares the proxy's address.
func clientAddress(r *http.Request) string {
	for _, header := range []string{trueClientIPHeader, realIPHeader} {
		if addr := strings.TrimSpace(r.Header.Get(header)); addr != "" {
			return addr
		}
	}
	if forwarded := r.Header.Get(forwardedForHeader); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if addr := strings.TrimSpace(first); addr != "" {
			return addr
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}
