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
// may ask one before signing in: the estimate reads a password's first MaxJudged characters and
// each detail's first maxDetail, a detail cut before it is normalized so a long one is never
// read whole, and it reads look-alike characters as letters (4 for a, $ for s) while the passes
// that reading takes, each costing the square of the length, cost no more than maxL33tWork
// passes over MaxJudged characters. A password past that holds many different look-alike
// characters for its length, and is estimated as typed. The password is read as typed, one
// character for each character typed, and in the other ways a person's word may be written in
// it; the library is asked about each and the lowest estimate counts, so another way of reading
// a password never makes it stronger. The library does not read look-alike characters against
// the person's words, so a password that spells a word of theirs of minLookAlike characters or
// more with look-alikes standing for some of its letters, beside at least one of its letters
// typed as itself, is also read with the word written as the person wrote it. That reading runs
// both ways: a look-alike in the password stands for the word's letter (Z@br0w$k! for
// Zabrowski), and one in the word for the letter typed (Zabrowski for z4br0wski). Finding the
// words compares each one with the password at each of its first MaxJudged characters, no
// further than MaxJudged characters each time. A password that is one piece typed again and
// again (abababab) is also read as the piece and how many times it is typed, since the library
// sees one character typed again and again and nothing longer. The asks about the password as
// typed, with the person's details and, when it falls short, without them to name the reason,
// are each within maxL33tCost, and the others, the piece among them, within maxL33tCostFurther,
// at most maxFurtherAsks of them, so one judgment costs no more than three asks at the bound.
// The list is asked about the whole password, which is hashed in pieces and never copied.
//
// The library counts bytes where a person types characters, so it is asked about a text in
// ASCII alone: an accented letter is read as its letter, a dotless ı as i, a letter people also
// write in plain letters as that spelling (ß as ss, ł as l, ø as o, æ as ae), a letter people
// write two ways in plain letters in both (ü as u and ue, ø as o and oe, å as a and aa, đ as d
// and dj), and any other character outside ASCII as one stand-in, the same for the same letter,
// with or without its marks and in either case, throughout one judgment. A letter read as two
// (ß, æ, œ, þ) is written as its two letters to match a person's word, and counts as one
// character toward the password's strength: the password as typed has it as its stand-in. A
// vowel sign or tone mark that a script types as a character of its own, as Thai and Hindi do,
// is a character: it counts toward the password's strength, and a name written with it counts
// against a password built on it. A point a person may leave off (a Hebrew, Arabic or Syriac
// point, an Indic nukta) is no character, so a password or a name counts the same with or
// without its points.
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

	// maxFurtherAsks is the most asks of one judgment past the two about the password as
	// typed: with the person's details, about the password as spelled, in its second spelling
	// and with a repeated piece typed once, and the two plain readings; without them, about the
	// first three.
	maxFurtherAsks = 8

	// maxL33tCostFurther is the most the reading of look-alike characters may cost in each of
	// the further asks, so that together they cost no more than one ask about the password as
	// typed.
	maxL33tCostFurther = maxL33tCost / maxFurtherAsks

	// maxDetail is how many characters of each of the person's details, trimmed and in NFC,
	// count against a password.
	maxDetail = 256

	// maxDetailTyped is how many characters of each of the person's details, as typed, are
	// trimmed and put in NFC: a character in NFC is made of a few typed ones at most (a letter
	// and its marks, the parts of a Hangul syllable), so these give maxDetail.
	maxDetailTyped = 4 * maxDetail

	// minRun is the shortest run of letters and digits in a person's detail that counts on
	// its own against a password.
	minRun = 3

	// minLookAlike is the shortest word of the person's read in look-alike characters: a
	// shorter one is spelled by chance in digits (471 for Ali) too often to count.
	minLookAlike = 4

	// nuktaClass is the canonical combining class of a nukta, the dot some Indic letters take
	// for a borrowed sound.
	nuktaClass = 7

	// myanmarDotBelow is Myanmar's dot below, the mark of the creaky tone. Unicode gives it the
	// nukta's class, and it is a tone mark a person types as a character.
	myanmarDotBelow = '\u1037'

	// chandrabindu and anusvara are Devanagari's signs of a nasal vowel; Hindi writes the
	// anusvara in the chandrabindu's place, so the reading gives the two one stand-in.
	chandrabindu, anusvara = '\u0901', '\u0902'

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
	typed, spelled, second := spellingsOf(password, read)
	inputs := inputsOf(who, read)
	readings := readingsOf(typed, spelled, second, inputs)
	bits := bitsOf(readings, inputs)
	strength := strengthOf(bits, minimum)
	if bits >= float64(minimum) {
		return Judgment{StrongEnough: true, Strength: max(strength, Mark), Reason: ""}
	}

	// Without inputs the asks without them would answer as the asks with them did.
	reason := ReasonGuessable
	if inputs != nil && reachesWithoutDetails(readings, minimum) {
		reason = ReasonPersonal
	}

	return Judgment{StrongEnough: false, Strength: min(strength, Mark-1), Reason: reason}
}

// spelling says how reading.as writes a letter people also write in plain letters.
type spelling int

const (
	// spelledPlain writes each such letter in its plain spelling (ß as ss): the reading of the
	// person's details.
	spelledPlain spelling = iota

	// spelledOnce writes one character for each character typed: a letter whose plain spelling
	// is two letters (ß, æ, œ, þ) is written as its stand-in.
	spelledOnce

	// spelledSecond writes a letter that has a second plain spelling (alsoSpelledInASCII) in
	// that one, and every other character as spelledPlain does.
	spelledSecond
)

// asked is one reading of the password the library is asked about.
type asked struct {
	// text is the reading, in ASCII.
	text string

	// added is what is added to the library's estimate of text, in bits.
	added float64

	// bound is the most the library's reading of text's look-alike characters may cost.
	bound int

	// personal says the reading is made from the person's words, so it is not asked without them.
	personal bool
}

// spellingsOf are the three texts password is read as, each cut to MaxJudged characters:
// typed, one character for each of the password's first MaxJudged characters (spelledOnce);
// spelled, the same characters with each letter people also write in plain letters in that
// spelling, as the person's details are read; and second, with each letter that has a second
// plain spelling in that one (spelledSecond). They are read in that order, so the stand-ins are
// given in the order the password's characters are typed.
func spellingsOf(password string, read *reading) (typed, spelled, second string) {
	cut := firstRunes(password, MaxJudged)
	typed = firstRunes(read.as(cut, spelledOnce), MaxJudged)
	spelled = firstRunes(read.of(cut), MaxJudged)
	second = firstRunes(read.as(cut, spelledSecond), MaxJudged)

	return typed, spelled, second
}

// readingsOf are the readings of the password the library is asked about, in this order, a text
// already among them left out: typed, within maxL33tCost; spelled; second; typed with its run
// of one piece typed again and again written as the piece once (typedOnce), with the bits of how
// many times it is typed added; and spelled and second each with the person's words it spells
// with look-alike characters written as the words are (plainReading), personal. Every reading
// after typed is within maxL33tCostFurther.
// Each spelling has its own plain reading so a word of the person's keeps its place in the
// list in whichever spelling the person wrote it: Süßmilch typed for a person named Süßmilch
// matches sussmilch, and for one registered as Suessmilch, suessmilch.
func readingsOf(typed, spelled, second string, inputs []string) []asked {
	readings := []asked{{text: typed, added: 0, bound: maxL33tCost, personal: false}}
	add := func(text string, added float64, personal bool) {
		for _, r := range readings {
			if r.text == text {
				return
			}
		}
		readings = append(readings, asked{text: text, added: added, bound: maxL33tCostFurther, personal: personal})
	}
	add(spelled, 0, false)
	add(second, 0, false)
	if once, times := typedOnce(typed); times > 1 {
		add(once, math.Log2(float64(times)), false)
	}
	add(plainReading(spelled, inputs), 0, true)
	add(plainReading(second, inputs), 0, true)

	return readings
}

// typedOnce is text with the run of one piece typed again and again that covers the most of it
// written as the piece once, and how many times the piece is typed in the run: abababab is ab,
// 4. A piece is two characters or more, since the library sees one character typed again and
// again itself, and stands whole each time. Of runs that cover as much, the one that starts
// first is taken, and of those the one with the shortest piece. With no run, it is text and 1.
// text is a reading, in ASCII and at most MaxJudged characters long, so a byte is a character
// and the search is bounded.
func typedOnce(text string) (once string, times int) {
	start, piece, times := 0, 0, 1
	for i := range len(text) {
		for p := 2; i+2*p <= len(text); p++ {
			n := 1
			for i+(n+1)*p <= len(text) && text[i+n*p:i+(n+1)*p] == text[i:i+p] {
				n++
			}
			if n > 1 && n*p > times*piece {
				start, piece, times = i, p, n
			}
		}
	}
	if piece == 0 {
		return text, 1
	}

	return text[:start+piece] + text[start+times*piece:], times
}

// bitsOf is the lowest of the library's estimates of the readings with inputs, each with what
// the reading adds: another way of reading the password never makes it stronger.
func bitsOf(readings []asked, inputs []string) float64 {
	bits := math.Inf(1)
	for _, r := range readings {
		bits = min(bits, estimate(r.text, inputs, r.bound)+r.added)
	}

	return bits
}

// reachesWithoutDetails says whether every reading not made from the person's words reaches
// minimum when the library is asked without the person's details; it stops at the first that
// does not.
func reachesWithoutDetails(readings []asked, minimum int) bool {
	for _, r := range readings {
		if !r.personal && estimate(r.text, nil, r.bound)+r.added < float64(minimum) {
			return false
		}
	}

	return true
}

// estimate is the library's estimate of how hard judged is to guess, in bits, with inputs
// among the words an attacker tries first. When reading judged's look-alike characters as
// letters would cost more than bound, the library is asked without that reading.
func estimate(judged string, inputs []string, bound int) float64 {
	if !readsLookAlikes(judged, bound) {
		return zxcvbn.PasswordStrength(judged, inputs, matching.FilterL33tMatcher).Entropy
	}

	return zxcvbn.PasswordStrength(judged, inputs).Entropy
}

// readsLookAlikes says whether the estimate reads judged's look-alike characters as letters:
// whether the passes that reading takes, times the square of judged's length, are within
// bound. judged is in ASCII, so its length in bytes is its length in characters.
func readsLookAlikes(judged string, bound int) bool {
	n := len(judged)

	return l33tWork(judged)*n*n <= bound
}

// strengthOf places bits on the scale from 0 to Full, with minimum at Mark.
func strengthOf(bits float64, minimum int) int {
	strength := math.Floor(Mark * bits / float64(minimum))

	return int(max(0, min(strength, Full)))
}

// firstRunes is s cut to its first n characters, as typed. It reads s from its start and no
// further than the cut, so a long text costs no more than its first characters; a byte that is
// not valid UTF-8 counts as one character, as ranging over s counts it.
func firstRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}

	return s
}

// inputsOf are the words the person's details give the estimate. Each detail is cut to its
// first maxDetailTyped characters, trimmed of surrounding space, put in NFC and cut to its
// first maxDetail characters; an empty one gives nothing, as does one with more than
// maxDetailTyped characters of space before anything else. The words come in two passes over
// the details, each in the order Username, Name, Email. First, each detail whole, then each run
// of letters, digits and marks in it whose reading is at least minRun characters long. Then,
// for each run, the run in its second spelling (Mueller in Müller, spelledSecond), and for a
// run that holds a digit, the run without the digits at its two ends (z4br0wski in z4br0wski77)
// and each part of it between digits (zabrowski in zabrowski77), each in both spellings; each
// of these whose reading is at least minRun characters long. The library prices a word by its
// place in the list, so the second pass comes after every word of the first, and a word of the
// first keeps the place it has without the second. Each word is as read reads it and given once
// whatever its case. It is nil when the details give none.
func inputsOf(who Details, read *reading) []string {
	var inputs []string
	seen := map[string]bool{}
	add := func(word string) {
		key := strings.ToLower(word)
		if word == "" || seen[key] {
			return
		}
		seen[key] = true
		inputs = append(inputs, word)
	}
	addRun := func(run string) {
		// The reading is in ASCII, so its length in bytes is its length in characters.
		if word := read.of(run); len(word) >= minRun {
			add(word)
		}
	}
	addBoth := func(text string) {
		// A text too short to count in its own spelling does not count in another.
		if len(read.of(text)) < minRun {
			return
		}
		add(read.of(text))
		add(read.as(text, spelledSecond))
	}

	var details []string
	for _, detail := range []string{who.Username, who.Name, who.Email} {
		// The detail is cut before it is trimmed or put in NFC, so a long one is never read whole.
		detail = firstRunes(norm.NFC.String(strings.TrimSpace(firstRunes(detail, maxDetailTyped))), maxDetail)
		if detail != "" {
			details = append(details, detail)
		}
	}
	for _, detail := range details {
		add(read.of(detail))
		for _, run := range strings.FieldsFunc(detail, notLetterDigitOrMark) {
			addRun(run)
		}
	}
	for _, detail := range details {
		for _, run := range strings.FieldsFunc(detail, notLetterDigitOrMark) {
			addBoth(run)
			if !strings.ContainsFunc(run, unicode.IsDigit) {
				continue
			}
			addBoth(strings.TrimFunc(run, unicode.IsDigit))
			for _, part := range strings.FieldsFunc(run, unicode.IsDigit) {
				addBoth(part)
			}
		}
	}

	return inputs
}

// plainReading is judged with each of the person's words it spells with look-alike characters,
// in the password or in the word, written as the word is, so the estimate finds the word:
// Z@br0w$k!2077 with the word Zabrowski is Zabrowski2077, and Zabrowski2077 with the word
// z4br0wski is Z4br0wski2077. The words are the inputs at least minLookAlike characters long,
// in lower case. From the left, at each character where words read (spellsAt), the longest is
// taken, the first in inputs of equal lengths: a character matched as itself stays as typed,
// one matched by look-alike becomes the word's character, in capitals when every letter of the
// word typed as itself there is a capital (Z@BR0W$K! is ZABROWSKI) and in lower case
// otherwise, and the reading goes on after the word. Where no word reads, the character
// stays. The plain reading is as long as judged, and is judged itself when no word reads
// anywhere in it. judged and the inputs are as read reads them, in ASCII, so a byte is a
// character.
func plainReading(judged string, inputs []string) string {
	var words []string
	for _, input := range inputs {
		if len(input) >= minLookAlike {
			words = append(words, strings.ToLower(input))
		}
	}
	if words == nil {
		return judged
	}

	plain := []byte(judged)
	for i := 0; i < len(judged); {
		longest := ""
		for _, word := range words {
			if len(word) > len(longest) && spellsAt(judged[i:], word) {
				longest = word
			}
		}
		if longest == "" {
			i++

			continue
		}
		// The look-alikes are written in capitals when the word's letters typed as themselves are.
		word := longest
		capitals := true
		for k := range len(longest) {
			if c := rune(judged[i+k]); sameInLowerCase(judged[i+k], longest[k]) && unicode.IsLetter(c) {
				capitals = capitals && unicode.IsUpper(c)
			}
		}
		if capitals {
			word = strings.ToUpper(longest)
		}
		for k := range len(longest) {
			if !sameInLowerCase(judged[i+k], longest[k]) {
				plain[i+k] = word[k]
			}
		}
		i += len(longest)
	}

	return string(plain)
}

// spellsAt says whether text begins with word with at least one of word's characters matched
// by look-alike and at least one of its letters typed as itself. Each of word's characters is
// matched in order, in one of three ways tried in this order: by a character that is the same
// in lower case; by one of the characters l33tTable lists for word's letter (a look-alike in
// the password); or, when word's character is one l33tTable lists for the letter typed (a
// look-alike in the word: 4 in z4br0wski for a typed a), by that letter. The second and third
// are look-alikes. A digit or a symbol of word typed as itself is no letter typed as itself,
// so digits alone never spell a word, and a word of digits is never spelled by letters. word
// is in lower case.
func spellsAt(text, word string) bool {
	if len(word) > len(text) {
		return false
	}
	lookAlike, asItself := false, false
	for k := range len(word) {
		switch {
		case sameInLowerCase(text[k], word[k]):
			asItself = asItself || unicode.IsLetter(rune(word[k]))
		case standsFor(text[k], word[k]), standsFor(word[k], text[k]):
			lookAlike = true
		default:
			return false
		}
	}

	return lookAlike && asItself
}

// sameInLowerCase says whether the ASCII characters a and b are the same in lower case.
func sameInLowerCase(a, b byte) bool {
	return unicode.ToLower(rune(a)) == unicode.ToLower(rune(b))
}

// standsFor says whether the character c is one l33tTable lists as standing for the letter,
// in either case.
func standsFor(c, letter byte) bool {
	for _, char := range l33tTable[strings.ToLower(string(letter))] {
		if char == string(c) {
			return true
		}
	}

	return false
}

// notLetterDigitOrMark says whether r ends a run of letters, digits and marks: a mark, a vowel
// sign of Thai or Hindi among them, is part of the word it is typed in.
func notLetterDigitOrMark(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
}

// reading makes the texts the library is asked about from what a person typed, in ASCII alone.
// One reading serves one judgment, the password first and then the person's details, so a
// character outside ASCII has one stand-in throughout.
type reading struct {
	standIn map[rune]byte
}

// newReading is a reading that has given no stand-in yet.
func newReading() *reading {
	return &reading{standIn: map[rune]byte{}}
}

// of is typed as the person's details are read: as(typed, spelledPlain).
func (r *reading) of(typed string) string {
	return r.as(typed, spelledPlain)
}

// as is typed in NFC as the library is to read it: a character in ASCII as it is; outside
// ASCII, under spelledSecond a letter that has a second plain spelling as that spelling
// (alsoSpelledInASCII), an accented letter or digit (é, Å, a full-width p) as its letter or
// digit, a letter people also write in plain letters as that spelling (spelledInASCII) or,
// under spelledOnce and when that spelling is two letters, as its stand-in, a letter whose
// capital is in ASCII (the dotless ı) as that letter in lower case, a mark NFC left on its own
// dropped when it is one a person may leave off (leftOff), a chandrabindu as an anusvara, and
// any other character as its stand-in, a vowel sign or tone mark that a script types as a
// character of its own (Thai, Hindi) among them.
func (r *reading) as(typed string, how spelling) string {
	var text strings.Builder
	for _, c := range norm.NFC.String(typed) {
		if c < utf8.RuneSelf {
			text.WriteRune(c)

			continue
		}
		// Before baseOf, since ä, ö, ü and å have a letter they are written on.
		if second, ok := alsoSpelledInASCII[c]; ok && how == spelledSecond {
			text.WriteString(second)

			continue
		}
		if base := baseOf(c); base != 0 {
			text.WriteRune(base)

			continue
		}
		if spelled, ok := spelledInASCII[c]; ok {
			if how == spelledOnce && len(spelled) > 1 {
				// A letter read as two counts once toward the strength.
				text.WriteByte(r.standInOf(c))
			} else {
				text.WriteString(spelled)
			}

			continue
		}
		if upper := unicode.ToUpper(c); upper < utf8.RuneSelf && unicode.IsLetter(upper) {
			text.WriteRune(unicode.ToLower(upper))

			continue
		}
		if unicode.IsMark(c) && leftOff(c) {
			// A mark a person may leave off is not a character a person counts.
			continue
		}
		if c == chandrabindu {
			c = anusvara
		}
		text.WriteByte(r.standInOf(c))
	}

	return text.String()
}

// leftOff says whether the mark c is one a person may leave off, so it is no character: a
// mark Unicode gives no script of its own (an accent typed apart from a letter it does not
// combine with), a point of Hebrew, Arabic or Syriac, scripts usually written without them,
// and a nukta. Myanmar's dot below has the nukta's class but is a tone mark, so it counts.
func leftOff(c rune) bool {
	if c == myanmarDotBelow {
		return false
	}

	return unicode.In(c, unicode.Inherited, unicode.Hebrew, unicode.Arabic, unicode.Syriac) ||
		norm.NFC.PropertiesString(string(c)).CCC() == nuktaClass
}

// spelledInASCII are the letters Unicode does not take apart that people also write in plain
// letters, with that spelling, in either case: one letter for ł, ø, đ, ð and ħ, two for ß, æ,
// œ and þ (the letters read as two).
var spelledInASCII = map[rune]string{
	'ß': "ss", 'ẞ': "SS",
	'ł': "l", 'Ł': "L",
	'ø': "o", 'Ø': "O",
	'đ': "d", 'Đ': "D",
	'ð': "d", 'Ð': "D",
	'ħ': "h", 'Ħ': "H",
	'æ': "ae", 'Æ': "AE",
	'œ': "oe", 'Œ': "OE", //nolint:goconst // ö and ø are also written oe (alsoSpelledInASCII): each entry spells its letter, and a constant would hide the spelling
	'þ': "th", 'Þ': "TH",
}

// alsoSpelledInASCII are the letters people write in plain letters two ways, with the longer
// spelling, in either case; the shorter is the letter itself (u for ü) or spelledInASCII's
// (o for ø).
var alsoSpelledInASCII = map[rune]string{
	'ä': "ae", 'Ä': "AE",
	'ö': "oe", 'Ö': "OE",
	'ü': "ue", 'Ü': "UE",
	'å': "aa", 'Å': "AA",
	'ø': "oe", 'Ø': "OE",
	'đ': "dj", 'Đ': "DJ",
}

// standInOf is c's stand-in, the same in either case and with or without c's marks: the one
// c's letter was given in this reading, or else the next of standIns. A stand-in is kept
// under the letter without its marks (ό under ο, ё under е), in the lower case of its upper
// case, so a capital and a final sigma have the letter's.
func (r *reading) standInOf(c rune) byte {
	lower := unicode.ToLower(unicode.ToUpper(unmarked(c)))
	if standIn, ok := r.standIn[lower]; ok {
		return standIn
	}
	standIn := standIns[len(r.standIn)%len(standIns)]
	r.standIn[lower] = standIn

	return standIn
}

// unmarked is c without its marks: the letter c is written on when c is that letter with
// only marks added (ό is ο, ё is е), and c itself when it is not.
func unmarked(c rune) rune {
	decomposed := []rune(norm.NFKD.String(string(c)))
	if len(decomposed) == 0 {
		return c
	}
	for _, mark := range decomposed[1:] {
		if !unicode.IsMark(mark) {
			return c
		}
	}

	return decomposed[0]
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
