// Package passwordstrength judges whether a password is strong enough to be set. It is the
// one judgment every path that sets a password asks and the one a page asks while a person
// types: a password is strong enough when the library's estimate of how hard it is to guess,
// in bits, reaches the application's minimum, with the person's own username, name and email
// address counted among the words an attacker tries first. The answer places the password on
// one scale from 0 to Full with the minimum at Mark, so a page draws it and calculates
// nothing, and names the reason a password falls short, which Sentence says to a person. A
// password is judged by its first MaxJudged characters, since the estimate's cost grows with
// the square of the length.
package passwordstrength

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	zxcvbn "github.com/ccojocar/zxcvbn-go"
)

const (
	// Mark is where the minimum sits on the scale a Judgment's Strength is on: a password is
	// strong enough exactly when its Strength has reached Mark.
	Mark = 50

	// Full is the top of the scale a Judgment's Strength is on.
	Full = 100

	// MaxJudged is how many characters of a password the estimate reads; the characters
	// after them change nothing in the answer.
	MaxJudged = 128

	// minRun is the shortest run of letters and digits in a person's detail that counts on
	// its own against a password.
	minRun = 3
)

// Reason says why a password falls short of the minimum; it is empty when the password does
// not.
type Reason string

const (
	// ReasonGuessable is a password too easy to guess, with or without the person's details.
	ReasonGuessable Reason = "guessable"

	// ReasonPersonal is a password that would reach the minimum but for the person's own
	// username, name or email address, which it is built on.
	ReasonPersonal Reason = "personal"
)

// Sentence is what a person reads about the reason: one sentence saying why the password
// falls short and what to do. It is empty for the empty reason.
func (r Reason) Sentence() string {
	switch r {
	case ReasonGuessable:
		return "This password is too easy to guess. Make it longer, or use several words that do not go together."
	case ReasonPersonal:
		return "This password is built on your name, username or email address. Choose one that is not."
	default:
		return ""
	}
}

// Details are what is known of the person a password is for, each counted against the
// password as a word an attacker tries first. An empty one counts for nothing.
type Details struct {
	Username string
	Name     string
	Email    string
}

// Judgment is the answer about one password under one minimum.
type Judgment struct {
	// StrongEnough says whether the password may be set; it is true exactly when Strength has
	// reached Mark.
	StrongEnough bool

	// Strength is where the password stands on a scale from 0 to Full, with the minimum at
	// Mark.
	Strength int

	// Reason says why the password falls short; it is empty when StrongEnough.
	Reason Reason
}

// Judge judges password against the person's details under minimum, in bits of the library's
// estimate. A minimum under 1 is a programmer's error, and Judge panics.
func Judge(password string, who Details, minimum int) Judgment {
	if minimum < 1 {
		panic("passwordstrength: the minimum is at least 1 bit")
	}
	if password == "" {
		return Judgment{StrongEnough: false, Strength: 0, Reason: ReasonGuessable}
	}

	judged := firstRunes(password, MaxJudged)
	bits := zxcvbn.PasswordStrength(judged, inputsOf(who)).Entropy
	strength := strengthOf(bits, minimum)
	if bits >= float64(minimum) {
		return Judgment{StrongEnough: true, Strength: max(strength, Mark), Reason: ""}
	}

	reason := ReasonGuessable
	if zxcvbn.PasswordStrength(judged, nil).Entropy >= float64(minimum) {
		reason = ReasonPersonal
	}

	return Judgment{StrongEnough: false, Strength: min(strength, Mark-1), Reason: reason}
}

// strengthOf places bits on the scale from 0 to Full, with minimum at Mark.
func strengthOf(bits float64, minimum int) int {
	strength := math.Floor(Mark * bits / float64(minimum))

	return int(max(0, min(strength, Full)))
}

// firstRunes is s cut to its first n characters, as typed.
func firstRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)

	return string(runes[:n])
}

// inputsOf are the words the person's details give the estimate, in the order Username, Name,
// Email: each detail whole, trimmed of surrounding space, then each run of letters and digits
// in it at least minRun characters long, each word once whatever its case. It is nil when the
// details give none.
func inputsOf(who Details) []string {
	var inputs []string
	seen := map[string]bool{}
	add := func(word string) {
		key := strings.ToLower(word)
		if seen[key] {
			return
		}
		seen[key] = true
		inputs = append(inputs, word)
	}

	for _, detail := range []string{who.Username, who.Name, who.Email} {
		detail = strings.TrimSpace(detail)
		if detail == "" {
			continue
		}
		add(detail)
		for _, run := range strings.FieldsFunc(detail, notLetterOrDigit) {
			if utf8.RuneCountInString(run) >= minRun {
				add(run)
			}
		}
	}

	return inputs
}

// notLetterOrDigit says whether r ends a run of letters and digits.
func notLetterOrDigit(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}
