package sessioninfo

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/impulseframework/impulse/httpio"
)

// MaxNameLength is the longest name a user holds, in characters.
const MaxNameLength = 200

// MaxUsernameLength is the longest username somebody may type, in characters: neither
// database's index on the username keeps every longer name.
const MaxUsernameLength = 200

// CleanName is a person's name as a user row keeps it: without the space around it, nil
// for none (nil, or nothing but space). A name longer than MaxNameLength, and one that
// is not text on one line (a control character in it, a line break and a tab among
// them, or bytes that are no text), are refused as a bad request. Everything that
// writes a name holds it to this one rule: the session library's own writes and the
// management an application generates for its people.
func CleanName(name *string) (*string, error) {
	if name == nil {
		return nil, nil
	}
	clean := strings.TrimSpace(*name)
	if clean == "" {
		return nil, nil
	}
	if !oneLineOfText(clean) {
		return nil, httpio.NewBadRequestMessage("a name is text on one line: it holds no line break, tab or other control character")
	}
	if utf8.RuneCountInString(clean) > MaxNameLength {
		return nil, httpio.NewBadRequestMessagef("a name is at most %d characters", MaxNameLength)
	}

	return &clean, nil
}

// KeptName is a name somebody else gave for a person (a directory at a sign-in, the
// sender of an invitation) as a user row would keep it: "" where CleanName keeps none
// and where it refuses the name. What the person did not type is dropped, never
// refused, so the sign-in it came with stands.
func KeptName(name string) string {
	clean, err := CleanName(&name)
	if err != nil || clean == nil {
		return ""
	}

	return *clean
}

// UsernameRefusal is the refusal, in words, of a username somebody typed that the
// databases do not all keep: one that is not text on one line, or of more than
// MaxUsernameLength characters. It is nil for any other name. A name nobody typed (a
// directory's, an address used as the username) is not held to it.
func UsernameRefusal(name string) error {
	if !oneLineOfText(name) {
		return httpio.NewBadRequestMessage("A username is text on one line, with no tab or other control character.")
	}
	if utf8.RuneCountInString(name) > MaxUsernameLength {
		return httpio.NewBadRequestMessagef("A username is at most %d characters.", MaxUsernameLength)
	}

	return nil
}

// oneLineOfText reports whether a value is text a row keeps and a page draws on one
// line: valid UTF-8 with no control character in it. A database refuses some of these
// outright (PostgreSQL keeps no NUL in text), so the rule refuses them first, in words.
func oneLineOfText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
