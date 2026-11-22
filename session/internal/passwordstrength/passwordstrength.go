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
// sees one character typed again and again and nothing longer. The look-alike characters of the
// password as typed are read whenever one ask of it costs no more than maxL33tCost, and those of
// each other reading when its asks also fit in what is left of maxL33tBudget: the readings
// anyone is asked first, each counted as two asks whatever the person's details are, then the
// readings made from the person's words. So one judgment costs no more than three asks at the
// bound, and a person's details never change how the password itself is read. The list is asked
// about the whole password, which is hashed in pieces and never copied.
//
// The library counts bytes where a person types characters, so it is asked about a text in
// ASCII alone: an accented letter is read as its letter, a dotless ı as i, a letter people
// also write in plain letters as that spelling (ß as ss, ł as l, ø as o, æ as ae), a letter
// people write two ways in plain letters in both (ü as u and ue, ø as o and oe, å as a and aa,
// đ as d and dj), and any other character outside ASCII as one stand-in, the same for the same
// letter, with or without its marks and in either case, throughout one judgment. A word of the
// person's with two such letters also counts with one in each spelling (Haegerstrom and
// Hagerstroem for Hägerström). A letter read as two (ß, æ, œ, þ) is written as its two letters
// to match a person's word, and counts as one character toward the password's strength: the
// password as typed has it as its stand-in. The person's words are given both ways, as typed
// first, so a name with such a letter matches the password as typed where it did before the
// letter was read as two. A vowel sign or tone mark that a script types as a character of its
// own, as Thai and Hindi do, is a character: it counts toward the password's strength, and a
// name written with it counts against a password built on it. A point a person may leave off
// (a Hebrew, Arabic or Syriac point, an Indic nukta) is no character, so a password or a name
// counts the same with or without its points.
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

	// maxL33tBudget is the most the readings of look-alike characters of one judgment may cost
	// together: three asks at the bound.
	maxL33tBudget = 3 * maxL33tCost

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

	// minTypedOnce is the fewest characters a run of one piece typed again and again covers. A
	// piece of two characters typed twice stands in ordinary words (eded in seeded, anan in
	// banana) and is left as typed.
	minTypedOnce = 6

	// maxTwoWay is the most letters with a second plain spelling a word of the person's may hold
	// and still be given in each mix of the two spellings: the ways double with each letter, and
	// every word is compared with the password at each of its characters.
	maxTwoWay = 2

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

	// added is what is added to the library's estimate of text, in bits. Only the reading with
	// the piece written once adds any.
	added float64

	// bound is maxL33tCost when the library reads text's look-alike characters as letters, and
	// 0 when it does not (setBounds).
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
// already among them left out: typed; spelled; second; typed with its run of one piece typed
// again and again written as the piece once (typedOnce), with the bits of how many times it is
// typed added; and spelled and second each with the person's words it spells with look-alike
// characters written as the words are (plainReading), personal. setBounds says which of them
// are read with their look-alike characters.
// Each spelling has its own plain reading so a word of the person's keeps its place in the
// list in whichever spelling the person wrote it: Süßmilch typed for a person named Süßmilch
// matches sussmilch, and for one registered as Suessmilch, suessmilch.
func readingsOf(typed, spelled, second string, inputs []string) []asked {
	readings := []asked{{text: typed, added: 0, bound: 0, personal: false}}
	add := func(text string, added float64, personal bool) {
		for _, r := range readings {
			if r.text == text {
				return
			}
		}
		readings = append(readings, asked{text: text, added: added, bound: 0, personal: personal})
	}
	add(spelled, 0, false)
	add(second, 0, false)
	if once, times := typedOnce(typed); times > 1 {
		add(once, math.Log2(float64(times)), false)
	}
	add(plainReading(spelled, inputs), 0, true)
	add(plainReading(second, inputs), 0, true)
	setBounds(readings)

	return readings
}

// setBounds gives each of readings its bound: maxL33tCost when the library is to read its
// look-alike characters, and 0 when it is not. A reading anyone is asked, one not made from the
// person's words, is counted as two asks whatever the person's details are: with the details
// and, when the password falls short, without them to name the reason. These come first, in
// their order, so the bound of each is the same whoever the person is: a person's details can
// only lower the estimate the password gets with none, and the reason named agrees with that
// judgment. The password as typed is the first of them: it is read with its look-alikes
// whenever one ask of it costs no more than maxL33tCost, exactly as before any other reading
// existed. Each of the others is read with them when one ask of it costs no more than
// maxL33tCost and its two asks fit in what is left of maxL33tBudget. The reading with the piece
// written once, the one with bits added, is there for the piece and not for a word of the
// person's, so it also leaves one ask at the bound. The readings made from the person's words
// come last, one ask each, in what is left: when the password's three spellings are one text,
// the first of them is read with its look-alikes whenever one ask of it costs no more than
// maxL33tCost.
func setBounds(readings []asked) {
	left := maxL33tBudget
	for _, personal := range []bool{false, true} {
		for i := range readings {
			r := &readings[i]
			if r.personal != personal {
				continue
			}
			asks, kept := 2, 0
			switch {
			case r.personal:
				asks = 1
			case r.added > 0:
				kept = maxL33tCost
			}
			cost := l33tCost(r.text)
			r.bound = 0
			if cost <= maxL33tCost && asks*cost <= left-kept {
				r.bound = maxL33tCost
				left -= asks * cost
			}
		}
	}
}

// typedOnce is text with the run of one piece typed again and again that covers the most of it
// written as the piece once, and how many times the piece is typed in the run: abababab is ab,
// 4. A piece is two characters or more, since the library sees one character typed again and
// again itself, and stands whole each time. A run covers minTypedOnce characters or more, so
// two characters typed twice inside a word (eded in seeded) are no run. Of runs that cover as
// much, the one that starts first is taken, and of those the one with the shortest piece. With
// no run, it is text and 1.
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
			if n > 1 && n*p >= minTypedOnce && n*p > times*piece {
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
// whether what that reading costs (l33tCost) is within bound.
func readsLookAlikes(judged string, bound int) bool {
	return l33tCost(judged) <= bound
}

// l33tCost is what the library's reading of text's look-alike characters as letters costs: the
// passes it takes times the square of text's length. text is in ASCII, so its length in bytes
// is its length in characters.
func l33tCost(text string) int {
	n := len(text)

	return l33tWork(text) * n * n
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
// maxDetailTyped characters of space before anything else. The words come in three passes over
// the details, each in the order Username, Name, Email. The first gives the words read as the
// password is typed (spelledOnce): each detail whole, then each run of letters and digits in it
// whose reading is at least minRun characters long, a mark ending a run (Adébáyọ and làdé in
// Adébáyọ̀ Ọ̀làdé, whose grave accents NFC leaves apart). That is how the words were made before a
// run with its marks counted as a word, so those words keep their places. The password as
// typed is asked with these in these places, so a name matches there with a letter read as two
// (ß, æ, œ, þ) counted once. The second gives each detail whole in its plain spelling, then,
// for each run of letters, digits and marks, and for a run that holds a digit also the run
// without the digits at its two ends (z4br0wski in z4br0wski77) and each part of it between
// digits (zabrowski in zabrowski77), the text as typed, in its plain spelling and in its second
// spelling (Mueller in Müller, spelledSecond), when its reading as typed is at least minRun
// characters long. The library prices a word by its place in the list, so every word of the
// second pass (a plain or second spelling, a run with a mark, a part between digits) comes after
// every word of the first, and a word of the first keeps the place it has without the second.
// The third gives each of the second's texts that counts in its mixed spellings (mixed), after
// every word of the first two. Each word is given once whatever its case. It is nil when the
// details give none.
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
	// counts says whether text is long enough, read as the password is typed, to count as a word.
	// That reading is in ASCII, so its length in bytes is its length in characters.
	counts := func(text string) bool {
		return len(read.as(text, spelledOnce)) >= minRun
	}

	var details []string
	for _, detail := range []string{who.Username, who.Name, who.Email} {
		// The detail is cut before it is trimmed or put in NFC, so a long one is never read whole.
		detail = firstRunes(norm.NFC.String(strings.TrimSpace(firstRunes(detail, maxDetailTyped))), maxDetail)
		if detail != "" {
			details = append(details, detail)
		}
	}
	// The first pass: the words as the password is typed. A mark ends a run here.
	for _, detail := range details {
		add(read.as(detail, spelledOnce))
		for _, run := range strings.FieldsFunc(detail, notLetterOrDigit) {
			if counts(run) {
				add(read.as(run, spelledOnce))
			}
		}
	}

	// The second pass: the other spellings, the runs with a mark and the parts between digits.
	var texts []string
	for _, detail := range details {
		add(read.of(detail))
		for _, run := range strings.FieldsFunc(detail, notLetterDigitOrMark) {
			texts = append(texts, run)
			if strings.ContainsFunc(run, unicode.IsDigit) {
				texts = append(texts, strings.TrimFunc(run, unicode.IsDigit))
				texts = append(texts, strings.FieldsFunc(run, unicode.IsDigit)...)
			}
		}
	}
	for _, text := range texts {
		if !counts(text) {
			continue
		}
		add(read.as(text, spelledOnce))
		add(read.of(text))
		add(read.as(text, spelledSecond))
	}

	// The third pass: the mixed spellings (mixed) are the newest words, so they come last and no
	// word of the first two passes moves.
	for _, text := range texts {
		if !counts(text) {
			continue
		}
		for _, way := range read.mixed(text) {
			add(way)
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

// notLetterOrDigit says whether r ends a run of letters and digits: a mark ends one.
func notLetterOrDigit(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
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
// character of its own (Thai, Hindi) among them. A spelling from either table is written in
// the case of the word it stands in (inCase).
func (r *reading) as(typed string, how spelling) string {
	return r.spell(typed, how, func(int) bool {
		return how == spelledSecond
	})
}

// spell is typed as as reads it under how, but for the letters that have a second plain
// spelling (alsoSpelledInASCII): counted from 0 in the order they are met, the letter numbered
// n is written in its second spelling when second(n), and as how says otherwise.
func (r *reading) spell(typed string, how spelling, second func(n int) bool) string {
	var text strings.Builder
	// typed is a password cut to MaxJudged characters or a detail cut to maxDetail, so the
	// slice is small.
	chars := []rune(norm.NFC.String(typed))
	twoWay := 0
	for i, c := range chars {
		if c < utf8.RuneSelf {
			text.WriteRune(c)

			continue
		}
		// Before baseOf, since ä, ö, ü and å have a letter they are written on.
		if also, ok := alsoSpelledInASCII[c]; ok {
			twoWay++
			if second(twoWay - 1) {
				text.WriteString(inCase(also, chars, i))

				continue
			}
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
				text.WriteString(inCase(spelled, chars, i))
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

// mixed are the mixed ways of text, a word of the person's with two letters or more that have a
// second plain spelling, each in its plain spelling (spell, spelledPlain) but for some of those
// letters, which are in their second: way w, from 1 to two to the number of letters less 2,
// writes letter n in its second spelling when bit n of w is set. With two such letters these
// are the first in its second spelling and the second in its first, then the other way round
// (Haegerstrom and Hagerstroem for Hägerström). It is nil for a word with fewer than two such
// letters, whose spellings the second pass gives, or with more than maxTwoWay.
func (r *reading) mixed(text string) []string {
	letters := 0
	for _, c := range norm.NFC.String(text) {
		if _, ok := alsoSpelledInASCII[c]; ok {
			letters++
		}
	}
	if letters < 2 || letters > maxTwoWay {
		return nil
	}
	var ways []string
	for w := 1; w <= 1<<letters-2; w++ {
		ways = append(ways, r.spell(text, spelledPlain, func(n int) bool {
			return w>>n&1 == 1
		}))
	}

	return ways
}

// inCase is spelling, the plain letters the letter chars[i] is written as, in the case of the
// word it stands in. For a capital, the word's case is the case of the letter after it, or of
// the letter before it when no letter comes after; with no letter beside it, the spelling is as
// the table has it. A ß after the capital is passed over, since it has no capital to tell by:
// the Ü of SÜßMILCH is UE by the M after the ß, and the Ä of GEFÄß is AE by the F before it. A
// capital in a word not typed in capitals keeps one capital (Ü is Ue in Übel and UE in ÜBEL):
// two capitals where a person typed one would cost the estimate a capital nobody typed. A ß has
// no capital on a keyboard, so in a word typed in capitals it is SS. A ß never begins a word, so
// its word is in capitals when the letter before it is a capital and no small letter follows it:
// SS in GROßKREUTZ and STRAUß, ss in FußBall, whose capital begins the next word, and in Aßmann,
// whose capital is the word's first.
func inCase(spelling string, chars []rune, i int) string {
	c := chars[i]
	if c == 'ß' {
		capitals := i > 0 && unicode.IsUpper(chars[i-1]) &&
			(i+1 == len(chars) || !unicode.IsLower(chars[i+1]))
		if capitals {
			return strings.ToUpper(spelling)
		}

		return spelling
	}
	// A ß has no capital to tell the word's case by, so the letter after it is asked.
	after := i + 1
	for after < len(chars) && chars[after] == 'ß' {
		after++
	}
	var beside rune
	switch {
	case after < len(chars) && unicode.IsLetter(chars[after]):
		beside = chars[after]
	case i > 0 && unicode.IsLetter(chars[i-1]):
		beside = chars[i-1]
	default:
		return spelling
	}
	if unicode.IsUpper(c) && !unicode.IsUpper(beside) {
		return spelling[:1] + strings.ToLower(spelling[1:])
	}

	return spelling
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
// œ and þ (the letters read as two). A capital's two letters are as written in a word in
// capitals; inCase writes them for any other.
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
// (o for ø). A capital's two letters are as written in a word in capitals; inCase writes them
// for any other.
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
