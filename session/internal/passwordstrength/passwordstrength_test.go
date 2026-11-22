package passwordstrength

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	zxcvbn "github.com/ccojocar/zxcvbn-go"
	"github.com/ccojocar/zxcvbn-go/matching"
	"golang.org/x/text/unicode/norm"
)

// calibration is the table the default minimum is set from: each password with no details,
// and whether it reaches 30 bits.
var calibration = []struct {
	password     string
	strongEnough bool
}{
	{"password", false},
	{"password1", false},
	{"12345678", false},
	{"qwertyuiop", false},
	{"zxcvbn", false},
	{"thx1138", false},
	{"rosebud", false},
	{"correcthorsebatterystaple", true},
	{"rWibMFACxAUGZmxhVncy", true},
	{"Ba9ZyWABu99[BK#6MBgbH88Tofv)vs$w", true},
	{"follow-the-lodestar-home", true},
	{"open-the-console-first", true},
}

// developmentPasswords are the passwords the repository's own seeds and tests sign in with;
// each must clear the default minimum with room to spare.
var developmentPasswords = []string{"follow-the-lodestar-home", "open-the-console-first"}

func TestJudge_calibration(t *testing.T) {
	t.Parallel()

	for _, tt := range calibration {
		t.Run(tt.password, func(t *testing.T) {
			t.Parallel()

			got := Judge(tt.password, Details{}, 30)
			if got.StrongEnough != tt.strongEnough {
				t.Errorf("Judge(%q, 30).StrongEnough = %v, want %v (%+v)", tt.password, got.StrongEnough, tt.strongEnough, got)
			}
			if tt.strongEnough && got.Reason != "" {
				t.Errorf("Judge(%q, 30).Reason = %q, want none", tt.password, got.Reason)
			}
			if !tt.strongEnough && got.Reason == "" {
				t.Errorf("Judge(%q, 30).Reason is empty, want why it falls short", tt.password)
			}
		})
	}
}

func TestJudge_developmentPasswordsHaveRoom(t *testing.T) {
	t.Parallel()

	for _, password := range developmentPasswords {
		if got := Judge(password, Details{}, 40); !got.StrongEnough {
			t.Errorf("Judge(%q, 40) = %+v, want it to measure 40 bits or more", password, got)
		}
	}
}

func TestJudge_strongEnoughExactlyAtTheMark(t *testing.T) {
	t.Parallel()

	for _, minimum := range []int{15, 30, 60} {
		for _, tt := range calibration {
			got := Judge(tt.password, Details{}, minimum)
			if got.StrongEnough != (got.Strength >= Mark) {
				t.Errorf("Judge(%q, %d) = %+v: StrongEnough disagrees with Strength against Mark %d", tt.password, minimum, got, Mark)
			}
			if got.Strength < 0 || got.Strength > Full {
				t.Errorf("Judge(%q, %d).Strength = %d, want it within 0 to %d", tt.password, minimum, got.Strength, Full)
			}
		}
	}
}

func TestJudge_personalDetails(t *testing.T) {
	t.Parallel()

	other := Details{Username: "someoneelse", Name: "Other Person", Email: "other@example.com"}
	tests := []struct {
		name     string
		password string
		who      Details
	}{
		{name: "the username", password: "vexillumtrek", who: Details{Username: "vexillumtrek"}},
		{name: "a part of the name", password: "Zabrowski2077", who: Details{Name: "Quentin Zabrowski"}},
		{name: "the address before the @", password: "marrowquiltfen", who: Details{Email: "marrowquiltfen@example.com"}},
		{name: "a detail in another case", password: "Vexillumtrek", who: Details{Username: "VEXILLUMTREK"}},
		{name: "the name with an accent the password lacks", password: "Zabrowski2077", who: Details{Name: "Quentin Zabrówski"}},
		{name: "the password with an accent the name lacks", password: "Zabrówski2077", who: Details{Name: "Quentin Zabrowski"}},
		{name: "a username outside ASCII", password: "щжфыхцэюбьъй", who: Details{Username: "щжфыхцэюбьъй"}},
		{name: "a username outside ASCII in another case", password: "ЩЖФЫХЦЭЮБЬЪЙ", who: Details{Username: "щжфыхцэюбьъй"}},
		{name: "a Greek name in capitals", password: "ΝΙΚΟΛΑΟΣ1990", who: Details{Name: "Νικόλαος Παπαδόπουλος"}},
		{name: "a name typed without a letter's mark", password: "Федоров1985!", who: Details{Name: "Фёдор Фёдоров"}},
		{name: "the name in look-alike characters", password: "Z@br0w$k!2077", who: Details{Name: "Quentin Zabrowski"}},
		{name: "a name with a dotless i, in capitals", password: "YILDIRIMKAYA2077", who: Details{Name: "Nur Yıldırımkaya"}},
		{name: "a name with a dotless i, typed with i", password: "yildirimkaya2077", who: Details{Name: "Nur Yıldırımkaya"}},
		{name: "a name with a ß, in capitals", password: "GROSSKREUTZ2077", who: Details{Name: "Jörg Großkreutz"}},
		{name: "a name with ss, typed with a ß", password: "Großkreutz2077", who: Details{Name: "Uwe Grosskreutz"}},
		{name: "a Thai name with its vowel signs and tone mark", password: "กิตติศักดิ์2077", who: Details{Name: "กิตติศักดิ์ ศรีสุวรรณ"}},
		{name: "a Hindi name with its vowel signs", password: "सुनीता2077!", who: Details{Name: "सुनीता शर्मा"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Judge(tt.password, Details{}, 30); !got.StrongEnough {
				t.Fatalf("Judge(%q) with no details = %+v, want strong enough: the case proves nothing", tt.password, got)
			}
			got := Judge(tt.password, tt.who, 30)
			if got.StrongEnough || got.Reason != ReasonPersonal {
				t.Errorf("Judge(%q, %+v) = %+v, want it short for %q", tt.password, tt.who, got, ReasonPersonal)
			}
			if got := Judge(tt.password, other, 30); !got.StrongEnough {
				t.Errorf("Judge(%q, %+v) = %+v, want strong enough with details it does not hold", tt.password, other, got)
			}
		})
	}
}

func TestPlainReading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		judged string
		words  []string
		want   string
	}{
		{judged: "Z@br0w$k!2077", words: []string{"Zabrowski"}, want: "Zabrowski2077"},
		{judged: "zabrowski2077", words: []string{"Zabrowski"}, want: "zabrowski2077"},
		{judged: "P@T77", words: []string{"pat"}, want: "PaT77"},
		{judged: "1!1!xx", words: []string{"lili"}, want: "lilixx"},
		{judged: "0'ne1l", words: []string{"O'Neil"}, want: "o'neil"},
		{judged: "@nn@belle", words: []string{"ann", "anna"}, want: "annabelle"},
		{judged: "@nn@nn", words: []string{"anna"}, want: "annann"},
		{judged: "xx@1", words: []string{"al"}, want: "xx@1"},
		{judged: "2077", words: []string{"zott"}, want: "zott"},
		{judged: "n1ck", words: []string{"nick"}, want: "nick"},
		{judged: "nick", words: []string{"n1ck"}, want: "nick"},
	}
	for _, tt := range tests {
		if got := plainReading(tt.judged, tt.words); got != tt.want {
			t.Errorf("plainReading(%q, %q) = %q, want %q", tt.judged, tt.words, got, tt.want)
		}
	}
}

func TestJudge_lookAlikesThatSpellNoDetailAreJudgedAsTyped(t *testing.T) {
	t.Parallel()

	const password, minimum = "Ba9ZyWABu99[BK#6MBgbH88Tofv)vs$w", 30
	who := Details{Name: "Quentin Zabrowski"}
	read := newReading()
	judged := read.of(password)
	inputs := inputsOf(who, read)
	if plain := plainReading(judged, inputs); plain != judged {
		t.Errorf("plainReading(%q, %q) = %q, want it as typed: no word of the person's is in it", judged, inputs, plain)
	}
	want := strengthOf(estimate(judged, inputs), minimum)
	if got := Judge(password, who, minimum); !got.StrongEnough || got.Strength != want {
		t.Errorf("Judge(%q, %+v) = %+v, want strong enough at the strength %d the library gives the password as typed", password, who, got, want)
	}
}

func TestJudge_weakWithOtherDetailsStaysGuessable(t *testing.T) {
	t.Parallel()

	who := Details{Username: "someoneelse", Name: "Other Person", Email: "other@example.com"}
	got := Judge("kestrelvaughn", who, 30)
	if got.StrongEnough || got.Reason != ReasonGuessable {
		t.Errorf("Judge(kestrelvaughn, %+v) = %+v, want it short for %q", who, got, ReasonGuessable)
	}
}

func TestJudge_minimumMovesStrength(t *testing.T) {
	t.Parallel()

	const password = "Tr0ub4dour&3"
	at30 := Judge(password, Details{}, 30)
	if higher := Judge(password, Details{}, 60); higher.Strength >= at30.Strength {
		t.Errorf("Strength at 60 = %d, want it under the %d at 30", higher.Strength, at30.Strength)
	}
	if lower := Judge(password, Details{}, 15); lower.Strength <= at30.Strength {
		t.Errorf("Strength at 15 = %d, want it over the %d at 30", lower.Strength, at30.Strength)
	}
}

func TestJudge_onlyTheFirstCharactersAreJudged(t *testing.T) {
	t.Parallel()

	weak := strings.Repeat("a", MaxJudged)
	for _, tail := range []string{"", "rWibMFACxAUGZmxhVncy", "Ba9ZyWABu99[BK#6MBgbH88Tofv)vs$w", "é"} {
		if got, want := Judge(weak+tail, Details{}, 30), Judge(weak, Details{}, 30); got != want {
			t.Errorf("Judge(%d a's + %q) = %+v, want %+v as without the tail", MaxJudged, tail, got, want)
		}
	}
}

func TestJudge_theReadingIsCutToTheFirstCharacters(t *testing.T) {
	t.Parallel()

	// A ß reads as two characters, so 64 of them read as 128: only the first 64 are judged.
	long, short := strings.Repeat("ß", MaxJudged), strings.Repeat("ß", MaxJudged/2)
	if got, want := Judge(long, Details{}, 30), Judge(short, Details{}, 30); got != want {
		t.Errorf("Judge(%d ß) = %+v, want %+v as for %d ß: both are %d s to the library", MaxJudged, got, want, MaxJudged/2, MaxJudged)
	}
}

func TestL33tWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		read string
		want int
	}{
		{"password", 1},
		{"p@ssw0rd", 1},
		{"Tr0ub4dour&3", 1},
		{"qwER43@!", 2},
		{"neverforget13/3/1997", 8},
		{"0123456789", 16},
		{"4@8({[<3691!|70$5+%2", 4608},
	}
	for _, tt := range tests {
		if got := l33tWork(tt.read); got != tt.want {
			t.Errorf("l33tWork(%q) = %d, want %d", tt.read, got, tt.want)
		}
	}
}

func TestJudge_aCommonWordInLookAlikesFallsShort(t *testing.T) {
	t.Parallel()

	// Common words written in look-alike characters, with a year or symbols after them.
	for _, password := range []string{
		"P@ssw0rd!1976", "p@$$w0rd1|7", "5unsh1n3|7", "pr1nc3$$|7", "f00tb@ll|7!1", "m0nk3y1976!", "Footb@ll!1976",
	} {
		if got := Judge(password, Details{}, 30); got.StrongEnough || got.Reason != ReasonGuessable {
			t.Errorf("Judge(%q, 30) = %+v, want it short for %q", password, got, ReasonGuessable)
		}
	}
}

func TestJudge_aTextPastTheBoundIsEstimatedAsTyped(t *testing.T) {
	t.Parallel()

	// Many different look-alike characters for its length: 21 characters, 4,608 ways.
	const password, minimum = "p@$$w0rd4(5{[<69!|1+7", 100
	if readsLookAlikes(password) {
		t.Fatalf("l33tWork(%q) = %d at %d characters is within the bound: the case proves nothing", password, l33tWork(password), len(password))
	}
	filtered := strengthOf(zxcvbn.PasswordStrength(password, nil, matching.FilterL33tMatcher).Entropy, minimum)
	read := strengthOf(zxcvbn.PasswordStrength(password, nil).Entropy, minimum)
	if filtered == read {
		t.Fatalf("the library answers %q at strength %d with and without the look-alike reading: the case proves nothing", password, read)
	}
	if got := Judge(password, Details{}, minimum); got.Strength != filtered {
		t.Errorf("Judge(%q, %d).Strength = %d, want %d as the library answers without the look-alike reading (%d with it)", password, minimum, got.Strength, filtered, read)
	}
}

func TestJudge_calibrationIsUnderTheLookAlikeBound(t *testing.T) {
	t.Parallel()

	for _, tt := range calibration {
		if !readsLookAlikes(tt.password) {
			t.Errorf("l33tWork(%q) = %d at %d characters is past the bound, want it within, so the table is judged with the look-alike reading", tt.password, l33tWork(tt.password), len(tt.password))
		}
	}
}

func TestJudge_detailsAreReadByTheirFirstCharacters(t *testing.T) {
	t.Parallel()

	const password = "vexillumtrek"
	who := Details{Username: strings.Repeat("a ", 128) + password}
	if got := Judge(password, who, 30); !got.StrongEnough {
		t.Errorf("Judge(%q) with the word past the username's %dth character = %+v, want strong enough", password, maxDetail, got)
	}
}

func TestJudge_aLongDetailCostsNoMoreThanItsFirstCharacters(t *testing.T) { //nolint:paralleltest // it counts what the whole process allocates
	const password, minimum = "vexillumtrek", 30
	long := strings.Repeat("שּׁ", 4<<20/3)
	first := firstRunes(long, maxDetailTyped)
	allocated := func(name string) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		Judge(password, Details{Name: name}, minimum)
		runtime.ReadMemStats(&after)

		return after.TotalAlloc - before.TotalAlloc
	}
	allocated(first)
	short := allocated(first)
	if got := allocated(long); got > short+64<<10 {
		t.Errorf("Judge with a name of %d bytes allocated %d bytes, want no more than the %d of its first %d characters, within 64 KiB", len(long), got, short, maxDetailTyped)
	}
}

func TestFirstRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		n    int
		want string
	}{
		{s: "", n: 3, want: ""},
		{s: "abc", n: 3, want: "abc"},
		{s: "abcd", n: 3, want: "abc"},
		{s: "ééé", n: 2, want: "éé"},
		{s: "a\xffb", n: 2, want: "a\xff"},
	}
	for _, tt := range tests {
		if got := firstRunes(tt.s, tt.n); got != tt.want {
			t.Errorf("firstRunes(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

func TestJudge_aCharacterOutsideASCIICountsOnce(t *testing.T) {
	t.Parallel()

	for _, password := range []string{"éééééééé", "ββββββββ", "日日日日", "😀😀", "€€", "日本", "éèêë", "passwörd", strings.Repeat("😀", 64)} {
		if got := Judge(password, Details{}, 30); got.StrongEnough || got.Reason != ReasonGuessable {
			t.Errorf("Judge(%q, 30) = %+v, want it short for %q", password, got, ReasonGuessable)
		}
	}
}

func TestJudge_aScriptOutsideASCIICanMakeAStrongPassword(t *testing.T) {
	t.Parallel()

	const password = "щжфыхцэюбьъй"
	if got := Judge(password, Details{}, 30); !got.StrongEnough {
		t.Errorf("Judge(%q, 30) = %+v, want twelve different letters strong enough", password, got)
	}
}

func TestJudge_anAccentTypedApartIsJudgedAsTheAccentedLetter(t *testing.T) {
	t.Parallel()

	apart := strings.Repeat("e\u0301", 8)
	if got, want := Judge(apart, Details{}, 30), Judge("éééééééé", Details{}, 30); got != want {
		t.Errorf("Judge(%+q, 30) = %+v, want %+v as for %+q", apart, got, want, "éééééééé")
	}
}

func TestReading_of(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typed string
		want  string
	}{
		{typed: "Yıldız", want: "Yildiz"},
		{typed: "Strauß", want: "Strauss"},
		{typed: "STRAUẞ", want: "STRAUSS"},
		{typed: "İstanbul", want: "Istanbul"},
		{typed: "e\u0301", want: "e"},
		{typed: "x\u0301", want: "x"},
		{typed: "\u0e01\u0e34", want: standIns[:2]},
		{typed: "\u0915\u093f", want: standIns[:2]},
	}
	for _, tt := range tests {
		if got := newReading().of(tt.typed); got != tt.want {
			t.Errorf("of(%+q) = %q, want %q", tt.typed, got, tt.want)
		}
	}
}

func TestJudge_aVowelSignTypedApartCounts(t *testing.T) {
	t.Parallel()

	// Five Thai consonants, each with a different vowel sign, and the five alone.
	if got := Judge("กิขีคึงุจู", Details{}, 30); !got.StrongEnough {
		t.Errorf("Judge(%q, 30) = %+v, want strong enough: each vowel sign is a character typed", "กิขีคึงุจู", got)
	}
	if got := Judge("กขคงจ", Details{}, 30); got.StrongEnough {
		t.Errorf("Judge(%q, 30) = %+v, want it short", "กขคงจ", got)
	}
}

func TestJudge_aDetailInNFDIsJudgedAsInNFC(t *testing.T) {
	t.Parallel()

	const password, composed = "Sarka2077horse", "Šárka Nováková"
	decomposed := norm.NFD.String(composed)
	if decomposed == composed {
		t.Fatalf("NFD(%q) is the same text: the case proves nothing", composed)
	}
	got, want := Judge(password, Details{Name: decomposed}, 30), Judge(password, Details{Name: composed}, 30)
	if got != want {
		t.Errorf("Judge(%q, the name in NFD) = %+v, want %+v as with the name in NFC", password, got, want)
	}
	if want.StrongEnough {
		t.Errorf("Judge(%q, Name %q) = %+v, want it short: the password is built on the name", password, composed, want)
	}
}

func TestStandIns(t *testing.T) {
	t.Parallel()

	if n := utf8.RuneCountInString(standIns); n != 22 {
		t.Errorf("standIns holds %d characters, want 22", n)
	}
	seen := map[rune]bool{}
	for _, c := range standIns {
		if c >= utf8.RuneSelf {
			t.Errorf("standIns holds %q, which is not ASCII", c)
		}
		if seen[c] {
			t.Errorf("standIns holds %q twice", c)
		}
		seen[c] = true
	}
	for letter, chars := range l33tTable {
		for _, char := range chars {
			if strings.Contains(standIns, char) {
				t.Errorf("standIns holds %q, which the library reads as %q", char, letter)
			}
		}
	}
}

func TestJudge_emptyPassword(t *testing.T) {
	t.Parallel()

	want := Judgment{StrongEnough: false, Strength: 0, Reason: ReasonGuessable}
	if got := Judge("", Details{Username: "someone"}, 30); got != want {
		t.Errorf("Judge(\"\") = %+v, want %+v", got, want)
	}
}

func TestJudge_minimumUnderOnePanics(t *testing.T) {
	t.Parallel()

	for _, minimum := range []int{0, -1} {
		func() {
			defer func() {
				if got := recover(); got != "passwordstrength: the minimum is at least 1 bit" {
					t.Errorf("Judge with minimum %d panicked with %v, want the minimum's sentence", minimum, got)
				}
			}()
			Judge("correcthorsebatterystaple", Details{}, minimum)
		}()
	}
}

func TestReason_Sentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason Reason
		want   string
	}{
		{ReasonGuessable, "This password is too easy to guess. Make it longer, or use several words that do not go together."},
		{ReasonPersonal, "This password is built on your name, username or email address. Choose one that is not."},
		{ReasonCracked, "This password is on a list of passwords found in data breaches. Choose a different one."},
		{"", ""},
	}
	for _, tt := range tests {
		if got := tt.reason.Sentence(); got != tt.want {
			t.Errorf("Reason(%q).Sentence() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}
