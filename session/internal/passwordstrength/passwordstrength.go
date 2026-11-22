// Package passwordstrength judges whether a password is strong enough to be set. It is the
// one judgment every path that sets a password asks and the one a page asks while a person
// types: a password is strong enough when the library's estimate of how hard it is to guess,
// in bits, reaches the application's minimum, with the person's own username, name and email
// address counted among the words an attacker tries first, and it is not among the 100,000
// passwords most often found in data breaches (cracked.bin), which an attacker online tries
// before anything else. The answer places the password on one scale from 0 to Full with the
// minimum at Mark, so a page draws it and calculates nothing, and names the reason a password
// falls short, which Sentence says to a person.
//
// One judgment costs a bounded amount of work whatever the password is made of, since anyone
// may ask one before signing in: the estimate reads a password's first MaxJudged characters
// and each detail's first maxDetail, and it reads look-alike characters as letters (4 for a,
// $ for s) while the passes that reading takes, each costing the square of the length, cost
// no more than maxL33tWork passes over MaxJudged characters. A password past that holds many
// different look-alike characters for its length, and is estimated as typed. The list is
// asked about the whole password.
//
// The library counts bytes where a person types characters, so it is asked about a text in
// ASCII alone, one character for each character typed: an accented letter is read as its
// letter, and any other character outside ASCII as one stand-in, the same for the same
// character throughout one judgment.
package passwordstrength

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	zxcvbn "github.com/ccojocar/zxcvbn-go"
	"github.com/ccojocar/zxcvbn-go/adjacency"
	"github.com/ccojocar/zxcvbn-go/matching"
	"golang.org/x/text/unicode/norm"
)

const (
	// Mark is where the minimum sits on the scale a Judgment's Strength is on: a password is
	// strong enough exactly when its Strength has reached Mark.
	Mark = 50

	// Full is the top of the scale a Judgment's Strength is on.
	Full = 100

	// MaxJudged is how many characters of a password the estimate reads; the characters
	// after them change nothing in the answer. It is 64, longer than the passphrases people
	// type, because the estimate's work grows with the square of the length and is done once
	// for every way of reading the password's look-alike characters.
	MaxJudged = 64

	// maxL33tWork is the most passes over a text of MaxJudged characters the estimate's
	// reading of look-alike characters as letters may take. A pass costs the square of the
	// length, so a shorter text may take more passes, by the square of how much shorter it is;
	// past that, the estimate reads the text as typed.
	maxL33tWork = 16

	// maxL33tCost is the most the reading of look-alike characters may cost, in passes times
	// the square of the length: maxL33tWork passes over MaxJudged characters.
	maxL33tCost = maxL33tWork * MaxJudged * MaxJudged

	// maxDetail is how many characters of each of the person's details, after the trim, count
	// against a password.
	maxDetail = 256

	// minRun is the shortest run of letters and digits in a person's detail that counts on
	// its own against a password.
	minRun = 3

	// standIns are the characters a character outside ASCII that is not an accented letter or
	// digit is read as, given in this order to each new such character of a judgment and
	// again from the first after the last. None is a character the library reads as a letter,
	// and no three in a row are neighbors on a keyboard.
	standIns = "~;^/*'#._:&,=\")>-?]`\\}"
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

	// ReasonCracked is a password on the list of passwords found in breaches, whatever its
	// estimate.
	ReasonCracked Reason = "cracked"
)

// Sentence is what a person reads about the reason: one sentence saying why the password
// falls short and what to do. It is empty for the empty reason.
func (r Reason) Sentence() string {
	switch r {
	case ReasonGuessable:
		return "This password is too easy to guess. Make it longer, or use several words that do not go together."
	case ReasonPersonal:
		return "This password is built on your name, username or email address. Choose one that is not."
	case ReasonCracked:
		return "This password is on a list of passwords found in data breaches. Choose a different one."
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
// estimate. A password on the list of passwords found in breaches is at 0 whatever the
// minimum. A minimum under 1 is a programmer's error, and Judge panics.
func Judge(password string, who Details, minimum int) Judgment {
	if minimum < 1 {
		panic("passwordstrength: the minimum is at least 1 bit")
	}
	if password == "" {
		return Judgment{StrongEnough: false, Strength: 0, Reason: ReasonGuessable}
	}
	if listed(password) {
		return Judgment{StrongEnough: false, Strength: 0, Reason: ReasonCracked}
	}

	read := newReading()
	judged := read.of(firstRunes(password, MaxJudged))
	inputs := inputsOf(who, read)
	bits := estimate(judged, inputs)
	strength := strengthOf(bits, minimum)
	if bits >= float64(minimum) {
		return Judgment{StrongEnough: true, Strength: max(strength, Mark), Reason: ""}
	}

	// Without inputs the second estimate would answer as the first.
	reason := ReasonGuessable
	if inputs != nil && estimate(judged, nil) >= float64(minimum) {
		reason = ReasonPersonal
	}

	return Judgment{StrongEnough: false, Strength: min(strength, Mark-1), Reason: reason}
}

// estimate is the library's estimate of how hard judged is to guess, in bits, with inputs
// among the words an attacker tries first. When reading judged's look-alike characters as
// letters would cost more than maxL33tCost, the library is asked without that reading.
func estimate(judged string, inputs []string) float64 {
	if !readsLookAlikes(judged) {
		return zxcvbn.PasswordStrength(judged, inputs, matching.FilterL33tMatcher).Entropy
	}

	return zxcvbn.PasswordStrength(judged, inputs).Entropy
}

// readsLookAlikes says whether the estimate reads judged's look-alike characters as letters:
// whether the passes that reading takes, times the square of judged's length, are within
// maxL33tCost. judged is in ASCII, so its length in bytes is its length in characters.
func readsLookAlikes(judged string) bool {
	n := len(judged)

	return l33tWork(judged)*n*n <= maxL33tCost
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
// Email: each detail trimmed of surrounding space and cut to its first maxDetail characters,
// whole, then each run of letters and digits in it at least minRun characters long, each word
// as read reads it and once whatever its case. It is nil when the details give none.
func inputsOf(who Details, read *reading) []string {
	var inputs []string
	seen := map[string]bool{}
	add := func(word string) {
		word = read.of(word)
		key := strings.ToLower(word)
		if word == "" || seen[key] {
			return
		}
		seen[key] = true
		inputs = append(inputs, word)
	}

	for _, detail := range []string{who.Username, who.Name, who.Email} {
		detail = firstRunes(strings.TrimSpace(detail), maxDetail)
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

// reading makes the text the library is asked about from what a person typed, in ASCII alone
// and one character for each character typed. One reading serves one judgment, the password
// first and then the person's details, so a character outside ASCII has one stand-in
// throughout.
type reading struct {
	standIn map[rune]byte
}

// newReading is a reading that has given no stand-in yet.
func newReading() *reading {
	return &reading{standIn: map[rune]byte{}}
}

// of is typed in NFC as the library is to read it: a character in ASCII as it is; outside
// ASCII, an accented letter or digit (é, Å, a full-width p) as its letter or digit, a mark
// on its own dropped, and any other character as its stand-in.
func (r *reading) of(typed string) string {
	var text strings.Builder
	for _, c := range norm.NFC.String(typed) {
		if c < utf8.RuneSelf {
			text.WriteRune(c)

			continue
		}
		if base := baseOf(c); base != 0 {
			text.WriteRune(base)

			continue
		}
		if unicode.IsMark(c) {
			// A mark on no letter is not a character a person counts.
			continue
		}
		text.WriteByte(r.standInOf(c))
	}

	return text.String()
}

// standInOf is c's stand-in: the one c was given in this reading, or else the next of
// standIns.
func (r *reading) standInOf(c rune) byte {
	if standIn, ok := r.standIn[c]; ok {
		return standIn
	}
	standIn := standIns[len(r.standIn)%len(standIns)]
	r.standIn[c] = standIn

	return standIn
}

// baseOf is the ASCII letter or digit c is written on, when c is that letter or digit with
// only marks added (é is e, Å is A, a full-width p is p), and 0 when it is not.
func baseOf(c rune) rune {
	decomposed := []rune(norm.NFKD.String(string(c)))
	if len(decomposed) == 0 || !asciiLetterOrDigit(decomposed[0]) {
		return 0
	}
	for _, mark := range decomposed[1:] {
		if !unicode.IsMark(mark) {
			return 0
		}
	}

	return decomposed[0]
}

// asciiLetterOrDigit says whether c is a letter or a digit of ASCII.
func asciiLetterOrDigit(c rune) bool {
	return c < utf8.RuneSelf && (unicode.IsLetter(c) || unicode.IsDigit(c))
}

// l33tTable is the library's table of look-alike characters: a letter to the characters that
// stand for it.
var l33tTable = adjacency.GraphMap["l33t"].Graph

// l33tWork is how many passes over read the library's reading of look-alike characters as
// letters takes, or more: it reads read once for each choice of one character for every
// letter that several of read's characters stand for, and again for each letter a character
// standing for several is given to. A text with none of the characters gives 1.
func l33tWork(read string) int {
	work := 1
	letters := map[string]int{}
	for _, chars := range l33tTable {
		present := 0
		for _, char := range chars {
			if strings.Contains(read, char) {
				present++
				letters[char]++
			}
		}
		if present > 1 {
			work *= present
		}
	}
	for _, n := range letters {
		if n > 1 {
			work *= n
		}
	}

	return work
}
