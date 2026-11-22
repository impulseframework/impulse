package passwordstrength

import (
	"math"
	"runtime"
	"slices"
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
		{name: "the name in capitals with look-alike characters", password: "Z@BR0W$K!2077xq", who: Details{Name: "Quentin Zabrowski"}},
		{name: "a name with a dotless i, in capitals", password: "YILDIRIMKAYA2077", who: Details{Name: "Nur Yıldırımkaya"}},
		{name: "a name with a dotless i, typed with i", password: "yildirimkaya2077", who: Details{Name: "Nur Yıldırımkaya"}},
		{name: "a name with a ß, in capitals", password: "GROSSKREUTZ2077", who: Details{Name: "Jörg Großkreutz"}},
		{name: "a name with ss, typed with a ß", password: "Großkreutz2077", who: Details{Name: "Uwe Grosskreutz"}},
		{name: "the letters of a username with digits after them", password: "Zabrowski2077!", who: Details{Username: "zabrowski77"}},
		{name: "the letters of a username with digits after them, in look-alike characters", password: "Z@br0w$k!2077", who: Details{Username: "zabrowski77"}},
		{name: "the name of a person whose username carries digits", password: "MariaReyeq9", who: Details{Username: "mreyes73", Name: "Maria Reyes"}},
		{name: "a name cut short, of a person whose username carries digits", password: "OlekKowalcz!", who: Details{Username: "olekk37", Name: "Olek Kowalczyk"}},
		{name: "a username with digits inside its spelling", password: "Z4br0wski2077!", who: Details{Username: "z4br0wski77"}},
		{name: "a username written with look-alike characters, typed in letters", password: "Zabrowski2077!", who: Details{Username: "z4br0wski77"}},
		{name: "a name with a ł, typed with l", password: "Lukasz2077horse", who: Details{Name: "Łukasz Błaszczykowski"}},
		{name: "a name with an ø, typed with o", password: "Soren2077horse", who: Details{Name: "Søren Kierkegaard"}},
		{name: "a name with ä and ö, typed with ae and oe", password: "Haegerstroem2077", who: Details{Name: "Ingrid Hägerström"}},
		{name: "a name with an ø, typed with oe", password: "Soeren2077horse", who: Details{Name: "Søren Kierkegaard"}},
		{name: "a name with an å, typed with aa", password: "Raagaard2077horse", who: Details{Name: "Håkon Rågård"}},
		{name: "a name with a đ, typed with dj", password: "djordjevic2077", who: Details{Name: "Đorđe Đorđević"}},
		{name: "a name with an ü, typed with ue", password: "Buechsenschuetz2077", who: Details{Name: "Günther Büchsenschütz"}},
		{name: "a name spelled with ue, typed with ü", password: "Büchsenschütz2077", who: Details{Name: "Uwe Buechsenschuetz"}},
		{name: "a Thai name with its vowel signs and tone mark", password: "กิตติศักดิ์2077", who: Details{Name: "กิตติศักดิ์ ศรีสุวรรณ"}},
		{name: "a Hindi name with its vowel signs", password: "सुनीता2077!", who: Details{Name: "सुनीता शर्मा"}},
		{name: "a Hindi name with a nukta, typed without it", password: "जाकिर2077!", who: Details{Name: "ज़ाकिर हुसैन"}},
		{name: "a Hindi name with a chandrabindu, typed with an anusvara", password: "चांदनी2077!", who: Details{Name: "चाँदनी शर्मा"}},
		{name: "the name in look-alike characters beside another word in them", password: "Z@br0w$k!f00tb@ll|1977", who: Details{Name: "Quentin Zabrowski"}},
		{name: "the name in look-alike characters after another word in them", password: "m0nk3y!Z@br0w$k!|1977", who: Details{Name: "Quentin Zabrowski"}},
		{name: "the name in look-alike characters after a second word in them", password: "sh@d0w!Z@br0w$k!|1977", who: Details{Name: "Quentin Zabrowski"}},
		{name: "a username in look-alike characters after a word in them and a piece typed three times", password: "w3|cOmEE1!1!1!mReY3573", who: Details{Username: "mreyes73"}},
		{name: "a short username in look-alike characters at the end", password: "shad0w|etme1nx!r+hor", who: Details{Username: "thor"}},
		{name: "a name typed with its ß beside a word in look-alike characters", password: "Großkreutz!m0nk3y|1977", who: Details{Name: "Jörg Großkreutz"}},
		{name: "a name typed with its þ beside a word in look-alike characters", password: "monk3y!þórsdóttir!1979", who: Details{Name: "Anna Þórsdóttir"}},
		{name: "a name with a þ, typed with it, beside two more such letters", password: "ÞórÆÆ1980", who: Details{Name: "Þór Guðmundsson"}},
		{name: "a name with an œ, typed with it, after a piece typed twice", password: "iz0riz0rŒhlen", who: Details{Name: "Ægir Œhlen"}},
		{name: "a name with a ß, typed in capitals around it", password: "$tapl3GROßKREUTZDe", who: Details{Name: "Jürgen Großkreutz"}},
		{name: "a part of the address, of a person whose name is written with vowel signs", password: "rahulv1eyXA", who: Details{Name: "राहुल शर्मा", Email: "rahul.sharma@example.com"}},
		{name: "a name spelled with Ue, typed with a capital Ü", password: "ÜbelackerP12pc", who: Details{Name: "Uwe Uebelacker"}},
		{name: "a name spelled with Oe, typed with a capital Ø", password: "ØyvindsenobwW", who: Details{Name: "Ola Oeyvindsen"}},
		{name: "a name spelled with Aa, typed with a capital Å", password: "ÅkerlundP?*u", who: Details{Name: "Per Aakerlund"}},
		{name: "a name spelled with Th, typed with a capital Þ", password: "ÞórsdóttirjEc%", who: Details{Name: "Anna Thorsdottir"}},
		{name: "a name spelled with Ae, typed with a capital Æ", password: "ÆrenlundBIFYo", who: Details{Name: "Per Aerenlund"}},
		{name: "a name with ä and ö, typed with ae and o", password: "Haegerstrom2077", who: Details{Name: "Ingrid Hägerström"}},
		{name: "a name with ä and ö, typed with a and oe", password: "Hagerstroem2077", who: Details{Name: "Ingrid Hägerström"}},
		{name: "a name with ä and ö, typed in capitals with AE and O", password: "HAEGERSTROM2077", who: Details{Name: "Ingrid Hägerström"}},
		{name: "a name with two đ, typed with d and dj", password: "Dordje2077horse", who: Details{Name: "Đorđe Đorđević"}},
		{name: "a name with two đ, typed with dj and d", password: "Djorde2077horse", who: Details{Name: "Đorđe Đorđević"}},
		{name: "a name with two ü, typed with ue and u", password: "Buechsenschutz2077", who: Details{Name: "Günther Büchsenschütz"}},
		{
			name:     "a name with an accent NFC leaves apart, of a person with an address",
			password: "Adebayo!x9Lq",
			who:      Details{Name: "Adébáyọ̀ Ọ̀làdé", Email: "ade@example.com"},
		},
		{
			name:     "the part of a name before an accent NFC leaves apart",
			password: "Chukwuẹs2077",
			who:      Details{Name: "Chukwuẹ̀mẹ́ka Ọ̀kọ̀nkwọ́"},
		},
		{
			name:     "the part of an Arabic name before its shadda",
			password: "محمT2077",
			who:      Details{Name: "محمّد علي"},
		},
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

func TestInputsOf(t *testing.T) {
	t.Parallel()

	whole := newReading().as("राहुल शर्मा", spelledOnce)
	halves := strings.Fields(whole)
	if len(halves) != 2 {
		t.Fatalf("the name %q reads %q, want two words: the case proves nothing", "राहुल शर्मा", whole)
	}
	tests := []struct {
		who  Details
		want []string
	}{
		{
			who:  Details{Username: "mreyes73", Name: "Maria Reyes", Email: "maria.reyes@example.com"},
			want: []string{"mreyes73", "Maria Reyes", "Maria", "Reyes", "maria.reyes@example.com", "example", "com", "mreyes"},
		},
		{who: Details{Username: "z4br0wski77"}, want: []string{"z4br0wski77", "z4br0wski", "wski"}},
		{who: Details{Username: "zab77rowski"}, want: []string{"zab77rowski", "zab", "rowski"}},
		{
			who:  Details{Username: "77zabrowski", Email: "zabrowski77@example.com"},
			want: []string{"77zabrowski", "zabrowski77@example.com", "zabrowski77", "example", "com", "zabrowski"},
		},
		{who: Details{Name: "Al"}, want: []string{"Al"}},
		{who: Details{Name: "Søren Müller"}, want: []string{"Soren Muller", "Soren", "Muller", "Soeren", "Mueller"}},
		{who: Details{Username: "müller77"}, want: []string{"muller77", "mueller77", "muller", "mueller"}},
		{who: Details{Name: "Lü Bu"}, want: []string{"Lu Bu"}},
		{
			who:  Details{Name: "Jörg Großkreutz"},
			want: []string{"Jorg Gro" + standIns[:1] + "kreutz", "Jorg", "Gro" + standIns[:1] + "kreutz", "Jorg Grosskreutz", "Joerg", "Grosskreutz"},
		},
		{
			who:  Details{Name: "राहुल शर्मा", Email: "rahul.sharma@example.com"},
			want: []string{whole, "rahul.sharma@example.com", "rahul", "sharma", "example", "com", halves[0], halves[1]},
		},
		{
			who:  Details{Name: "Ingrid Hägerström"},
			want: []string{"Ingrid Hagerstrom", "Ingrid", "Hagerstrom", "Haegerstroem", "Haegerstrom", "Hagerstroem"},
		},
		{
			who:  Details{Name: "Đorđe Đorđević"},
			want: []string{"Dorde Dordevic", "Dorde", "Dordevic", "Djordje", "Djordjevic", "Djorde", "Dordje", "Djordevic", "Dordjevic"},
		},
		{who: Details{Name: "Jääskeläinen"}, want: []string{"Jaaskelainen", "Jaeaeskelaeinen"}},
		{
			who:  Details{Name: "Adébáyọ̀ Ọ̀làdé", Email: "ade@example.com"},
			want: []string{"Adebayo Olade", "Adebayo", "lade", "ade@example.com", "ade", "example", "com", "Olade"},
		},
		{who: Details{}, want: nil},
	}
	for _, tt := range tests {
		if got := inputsOf(tt.who, newReading()); !slices.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
			t.Errorf("inputsOf(%+v) = %q, want %q", tt.who, got, tt.want)
		}
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
		{judged: "Z@BR0W$K!2077", words: []string{"Zabrowski"}, want: "ZABROWSKI2077"},
		{judged: "Z@bR0w$k!", words: []string{"Zabrowski"}, want: "ZabRowski"},
		{judged: "P@TTY77", words: []string{"patty"}, want: "PATTY77"},
		{judged: "P@T77", words: []string{"pat"}, want: "P@T77"},
		{judged: "l15a", words: []string{"lisa"}, want: "lisa"},
		{judged: "L15A", words: []string{"lisa"}, want: "LISA"},
		{judged: "1!1!xx", words: []string{"lili"}, want: "1!1!xx"},
		{judged: "2077", words: []string{"zott"}, want: "2077"},
		{judged: "0'ne1l", words: []string{"O'Neil"}, want: "o'neil"},
		{judged: "@nn@belle", words: []string{"ann", "anna"}, want: "annabelle"},
		{judged: "@nn@nn", words: []string{"anna"}, want: "annann"},
		{judged: "xx@1", words: []string{"al"}, want: "xx@1"},
		{judged: "n1ck", words: []string{"nick"}, want: "nick"},
		{judged: "nick", words: []string{"n1ck"}, want: "n1ck"},
		{judged: "Quartz9471&", words: []string{"ali"}, want: "Quartz9471&"},
		{judged: "kettle33$", words: []string{"lee"}, want: "kettle33$"},
		{judged: "A$dLxc0m", words: []string{"com"}, want: "A$dLxc0m"},
		{judged: "Zabrowski2077", words: []string{"z4br0wski"}, want: "Z4br0wski2077"},
		{judged: "Z@browski", words: []string{"z4br0wski"}, want: "Z@browski"},
		{judged: "xxiggoxx", words: []string{"1990"}, want: "xxiggoxx"},
	}
	for _, tt := range tests {
		if got := plainReading(tt.judged, tt.words); got != tt.want {
			t.Errorf("plainReading(%q, %q) = %q, want %q", tt.judged, tt.words, got, tt.want)
		}
	}
}

func TestJudge_digitsThatSpellAShortWordAreNotTheWord(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		password string
		who      Details
	}{
		{password: "Quartz9471&", who: Details{Name: "Ali Khan"}},
		{password: "kettle33$", who: Details{Name: "Grace Lee"}},
		{password: "A$dLxc0m", who: Details{Name: "Lisa Wong", Email: "lisa.wong@example.com"}},
	} {
		if got := Judge(tt.password, tt.who, 30); !got.StrongEnough {
			t.Errorf("Judge(%q, %+v) = %+v, want strong enough: it is not built on the person's details", tt.password, tt.who, got)
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
	want := strengthOf(estimate(judged, inputs, maxL33tCost), minimum)
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

func TestJudge_aLetterReadAsTwoCountsOnce(t *testing.T) {
	t.Parallel()

	other := Details{Username: "someoneelse", Name: "Other Person", Email: "other@example.com"}
	for _, password := range []string{"æææææ", "ææææææææææ", "œœœœœœœœœœ", "þþþþþþþþþþ", "ææææ1", "ß1ß1ß1ß"} {
		for _, who := range []Details{{}, other} {
			if got := Judge(password, who, 30); got.StrongEnough || got.Reason != ReasonGuessable {
				t.Errorf("Judge(%q, %+v) = %+v, want it short for %q: each letter is one key", password, who, got, ReasonGuessable)
			}
		}
	}
}

func TestJudge_aPieceTypedAgainAndAgainIsJudgedOnce(t *testing.T) {
	t.Parallel()

	other := Details{Username: "someoneelse", Name: "Other Person", Email: "other@example.com"}
	for _, password := range []string{"abababababab", "x7Kpx7Kp", "ก่ก่ก่ก่", "कीकीकीकीकी"} {
		for _, who := range []Details{{}, other} {
			if got := Judge(password, who, 30); got.StrongEnough || got.Reason != ReasonGuessable {
				t.Errorf("Judge(%q, %+v) = %+v, want it short for %q: it is one piece typed again and again", password, who, got, ReasonGuessable)
			}
		}
	}
	const strongTwice = "rWibMFACxAUGZmxhVncyrWibMFACxAUGZmxhVncy"
	if got := Judge(strongTwice, Details{}, 30); !got.StrongEnough {
		t.Errorf("Judge(%q, 30) = %+v, want a strong piece typed twice strong enough", strongTwice, got)
	}
	const seeded = "a seeded password"
	if got := Judge(seeded, Details{}, 30); !got.StrongEnough {
		t.Errorf("Judge(%q, 30) = %+v, want it strong enough: two characters typed twice inside a word are no piece typed again and again", seeded, got)
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
	if readsLookAlikes(password, maxL33tCost) {
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
		if !readsLookAlikes(tt.password, maxL33tCost) {
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
	// The username is cut to its space before it is trimmed, so nothing of it is left.
	who = Details{Username: strings.Repeat(" ", maxDetailTyped) + password}
	if got := Judge(password, who, 30); !got.StrongEnough {
		t.Errorf("Judge(%q) with the word after %d spaces in the username = %+v, want strong enough", password, maxDetailTyped, got)
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

func TestJudge_aLongPasswordAllocatesNoMoreThanItsFirstCharacters(t *testing.T) { //nolint:paralleltest // it counts what the whole process allocates
	const minimum = 30
	long := strings.Repeat("vexillumtrek", 4<<20/12)
	first := firstRunes(long, MaxJudged)
	allocated := func(password string) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		Judge(password, Details{}, minimum)
		runtime.ReadMemStats(&after)

		return after.TotalAlloc - before.TotalAlloc
	}
	allocated(first)
	short := allocated(first)
	if got := allocated(long); got > short+64<<10 {
		t.Errorf("Judge of a password of %d bytes allocated %d bytes, want no more than the %d of its first %d characters, within 64 KiB", len(long), got, short, MaxJudged)
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
		{typed: "Łukasz", want: "Lukasz"},
		{typed: "Søren", want: "Soren"},
		{typed: "Đorđe", want: "Dorde"},
		{typed: "Guðrún", want: "Gudrun"},
		{typed: "Ħal", want: "Hal"},
		{typed: "Ægir", want: "Aegir"},
		{typed: "Œuvre", want: "Oeuvre"},
		{typed: "Þór", want: "Thor"},
		{typed: "e\u0301", want: "e"},
		{typed: "x\u0301", want: "x"},
		{typed: "\u0e01\u0e34", want: standIns[:2]},
		{typed: "\u0915\u093f", want: standIns[:2]},
		{typed: "\u05e9\u05b8", want: standIns[:1]},
		{typed: "\u0710\u0730", want: standIns[:1]},
		{typed: "\u0628\u064e", want: standIns[:1]},
		{typed: "\u091c\u093c", want: standIns[:1]},
		{typed: "\u0915\u0901\u0915\u0902", want: standIns[:2] + standIns[:2]},
		{typed: "\u1014\u1031\u1037", want: standIns[:3]},
	}
	for _, tt := range tests {
		if got := newReading().of(tt.typed); got != tt.want {
			t.Errorf("of(%+q) = %q, want %q", tt.typed, got, tt.want)
		}
	}
}

func TestReading_as(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typed string
		how   spelling
		want  string
	}{
		{typed: "Strauß", how: spelledOnce, want: "Strau" + standIns[:1]},
		{typed: "STRAUẞ", how: spelledOnce, want: "STRAU" + standIns[:1]},
		{typed: "ßẞ", how: spelledOnce, want: standIns[:1] + standIns[:1]},
		{typed: "æœþ", how: spelledOnce, want: standIns[:3]},
		{typed: "Łukasz Søren", how: spelledOnce, want: "Lukasz Soren"},
		{typed: "Strauß", how: spelledPlain, want: "Strauss"},
		{typed: "Müller", how: spelledSecond, want: "Mueller"},
		{typed: "Hägerström", how: spelledSecond, want: "Haegerstroem"},
		{typed: "Søren", how: spelledSecond, want: "Soeren"},
		{typed: "Rågård", how: spelledSecond, want: "Raagaard"},
		{typed: "Đorđe", how: spelledSecond, want: "Djordje"},
		{typed: "ÄÖÜ", how: spelledSecond, want: "AEOEUE"},
		{typed: "Übelacker", how: spelledSecond, want: "Uebelacker"},
		{typed: "ÜBELACKER", how: spelledSecond, want: "UEBELACKER"},
		{typed: "Ü", how: spelledSecond, want: "UE"},
		{typed: "Ångström", how: spelledSecond, want: "Aangstroem"},
		{typed: "ÅNGSTRÖM", how: spelledSecond, want: "AANGSTROEM"},
		{typed: "Þór", how: spelledPlain, want: "Thor"},
		{typed: "Ægir", how: spelledPlain, want: "Aegir"},
		{typed: "Œuvre", how: spelledPlain, want: "Oeuvre"},
		{typed: "ÆÆ1980", how: spelledPlain, want: "AEAE1980"},
		{typed: "GROßKREUTZ", how: spelledPlain, want: "GROSSKREUTZ"},
		{typed: "STRAUß", how: spelledPlain, want: "STRAUSS"},
		{typed: "STRAUẞ", how: spelledPlain, want: "STRAUSS"},
		{typed: "Łukasz", how: spelledPlain, want: "Lukasz"},
		{typed: "über", how: spelledSecond, want: "ueber"},
		{typed: "Strauß", how: spelledSecond, want: "Strauss"},
		{typed: "Łukasz", how: spelledSecond, want: "Lukasz"},
		{typed: "José", how: spelledSecond, want: "Jose"},
	}
	for _, tt := range tests {
		if got := newReading().as(tt.typed, tt.how); got != tt.want {
			t.Errorf("as(%q, %d) = %q, want %q", tt.typed, tt.how, got, tt.want)
		}
	}
}

func TestReading_mixed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		ways []string
	}{
		{text: "Hägerström", ways: []string{"Haegerstrom", "Hagerstroem"}},
		{text: "Đorđe", ways: []string{"Djorde", "Dordje"}},
		{text: "Büchsenschütz", ways: []string{"Buechsenschutz", "Buchsenschuetz"}},
		{text: "ÅNGSTRÖM", ways: []string{"AANGSTROM", "ANGSTROEM"}},
		{text: "Müller", ways: nil},
		{text: "Jääskeläinen", ways: nil},
		{text: "Zabrowski", ways: nil},
	}
	for _, tt := range tests {
		if got := newReading().mixed(tt.text); !slices.Equal(got, tt.ways) || (got == nil) != (tt.ways == nil) {
			t.Errorf("mixed(%q) = %q, want %q", tt.text, got, tt.ways)
		}
	}
}

func TestSpellingsOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		password string
		typed    string
		spelled  string
		second   string
	}{
		{password: "vexillumtrek", typed: "vexillumtrek", spelled: "vexillumtrek", second: "vexillumtrek"},
		{password: "Großkreutz2077", typed: "Gro" + standIns[:1] + "kreutz2077", spelled: "Grosskreutz2077", second: "Grosskreutz2077"},
		{
			password: strings.Repeat("ß", MaxJudged),
			typed:    strings.Repeat(standIns[:1], MaxJudged),
			spelled:  strings.Repeat("s", MaxJudged),
			second:   strings.Repeat("s", MaxJudged),
		},
		{
			password: strings.Repeat("a", MaxJudged) + "ß",
			typed:    strings.Repeat("a", MaxJudged),
			spelled:  strings.Repeat("a", MaxJudged),
			second:   strings.Repeat("a", MaxJudged),
		},
		{password: "Müller2077", typed: "Muller2077", spelled: "Muller2077", second: "Mueller2077"},
		{password: "Süßmilch", typed: "Su" + standIns[:1] + "milch", spelled: "Sussmilch", second: "Suessmilch"},
		{
			password: strings.Repeat("ü", MaxJudged),
			typed:    strings.Repeat("u", MaxJudged),
			spelled:  strings.Repeat("u", MaxJudged),
			second:   strings.Repeat("ue", MaxJudged/2),
		},
	}
	for _, tt := range tests {
		typed, spelled, second := spellingsOf(tt.password, newReading())
		if typed != tt.typed || spelled != tt.spelled || second != tt.second {
			t.Errorf("spellingsOf(%q) = %q, %q, %q, want %q, %q, %q", tt.password, typed, spelled, second, tt.typed, tt.spelled, tt.second)
		}
	}
}

func TestTypedOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text  string
		once  string
		times int
	}{
		{text: "abababababab", once: "ab", times: 6},
		{text: "ababab", once: "ab", times: 3},
		{text: "abab", once: "abab", times: 1},
		{text: "ss1ss1ss1ss", once: "ss1ss", times: 3},
		{text: "xxabcabcabcyy", once: "xxabcyy", times: 3},
		{text: "xyxyx", once: "xyxyx", times: 1},
		{text: "abcdabcdxyxyxy", once: "abcdxyxyxy", times: 2},
		{text: "xyxyxyabcdabcd", once: "xyxyxyabcd", times: 2},
		{text: "ababcdcd", once: "ababcdcd", times: 1},
		{text: "aaaa", once: "aaaa", times: 1},
		{text: "x7Kpx7Kp", once: "x7Kp", times: 2},
		{text: "seeded", once: "seeded", times: 1},
		{text: "banana", once: "banana", times: 1},
		{text: "abcabd", once: "abcabd", times: 1},
		{text: "vexillumtrek", once: "vexillumtrek", times: 1},
		{text: "a", once: "a", times: 1},
		{text: "", once: "", times: 1},
	}
	for _, tt := range tests {
		if once, times := typedOnce(tt.text); once != tt.once || times != tt.times {
			t.Errorf("typedOnce(%q) = %q, %d, want %q, %d", tt.text, once, times, tt.once, tt.times)
		}
	}
}

func TestReadingsOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typed   string
		spelled string
		second  string
		inputs  []string
		want    []asked
	}{
		{
			typed: "vexillumtrek", spelled: "vexillumtrek", second: "vexillumtrek",
			want: []asked{{text: "vexillumtrek", bound: maxL33tCost}},
		},
		{
			typed: "Gro~kreutz2077", spelled: "Grosskreutz2077", second: "Grosskreutz2077",
			want: []asked{{text: "Gro~kreutz2077", bound: maxL33tCost}, {text: "Grosskreutz2077", bound: maxL33tCost}},
		},
		{
			typed: "Z@br0w$k!2077", spelled: "Z@br0w$k!2077", second: "Z@br0w$k!2077", inputs: []string{"Zabrowski"},
			want: []asked{{text: "Z@br0w$k!2077", bound: maxL33tCost}, {text: "Zabrowski2077", bound: maxL33tCost, personal: true}},
		},
		{
			typed: "Gr0~kreutz2077", spelled: "Gr0sskreutz2077", second: "Gr0sskreutz2077", inputs: []string{"Grosskreutz"},
			want: []asked{
				{text: "Gr0~kreutz2077", bound: maxL33tCost},
				{text: "Gr0sskreutz2077", bound: maxL33tCost},
				{text: "Grosskreutz2077", bound: maxL33tCost, personal: true},
			},
		},
		{
			typed: "Muller2077", spelled: "Muller2077", second: "Mueller2077",
			want: []asked{{text: "Muller2077", bound: maxL33tCost}, {text: "Mueller2077", bound: maxL33tCost}},
		},
		{
			typed: "Mull3r2077", spelled: "Mull3r2077", second: "Muell3r2077", inputs: []string{"Muller", "Mueller"},
			want: []asked{
				{text: "Mull3r2077", bound: maxL33tCost},
				{text: "Muell3r2077", bound: maxL33tCost},
				{text: "Muller2077", bound: maxL33tCost, personal: true},
				{text: "Mueller2077", bound: maxL33tCost, personal: true},
			},
		},
		{
			typed: "abababababab", spelled: "abababababab", second: "abababababab",
			want: []asked{{text: "abababababab", bound: maxL33tCost}, {text: "ab", added: math.Log2(6), bound: maxL33tCost}},
		},
	}
	for _, tt := range tests {
		if got := readingsOf(tt.typed, tt.spelled, tt.second, tt.inputs); !slices.Equal(got, tt.want) {
			t.Errorf("readingsOf(%q, %q, %q, %q) = %+v, want %+v", tt.typed, tt.spelled, tt.second, tt.inputs, got, tt.want)
		}
	}
}

func TestReadingsOf_aReadingAnyoneIsAskedIsTheSameWhoeverThePersonIs(t *testing.T) {
	t.Parallel()

	read := newReading()
	typed, spelled, second := spellingsOf("S|lve$+riabababab|I+7137", read)
	inputs := inputsOf(Details{Name: "Tessa Silvestri", Email: "tessa@example.com"}, read)
	without := readingsOf(typed, spelled, second, nil)
	with := readingsOf(typed, spelled, second, inputs)
	if len(with) <= len(without) {
		t.Fatalf("readingsOf with her words = %+v, no longer than without them = %+v: the case proves nothing", with, without)
	}
	if !slices.Equal(with[:len(without)], without) {
		t.Errorf("readingsOf with her words begins %+v, want the readings without them %+v", with[:len(without)], without)
	}
	for _, r := range with[len(without):] {
		if !r.personal {
			t.Errorf("readingsOf with her words has %+v after the readings without them, want only readings made from her words", r)
		}
	}
}

func TestSetBounds(t *testing.T) {
	t.Parallel()

	heavy := func(pad string) string { // 16 passes at 64 characters: maxL33tCost exactly
		return "4@({69$5" + strings.Repeat(pad, MaxJudged-8)
	}
	light := func(pad string) string { // one pass
		return strings.Repeat(pad, MaxJudged)
	}
	over := "4@({[69$5" + strings.Repeat("a", MaxJudged-9) // 24 passes: past maxL33tCost
	if got := l33tCost(heavy("a")); got != maxL33tCost {
		t.Fatalf("l33tCost(%q) = %d, want %d: the case proves nothing", heavy("a"), got, maxL33tCost)
	}
	if got := l33tCost(over); got <= maxL33tCost {
		t.Fatalf("l33tCost(%q) = %d, want more than %d: the case proves nothing", over, got, maxL33tCost)
	}
	if got := l33tCost(light("a")); got != MaxJudged*MaxJudged {
		t.Fatalf("l33tCost(%q) = %d, want %d: the case proves nothing", light("a"), got, MaxJudged*MaxJudged)
	}

	type given struct {
		text     string
		once     bool // the reading with the piece written once
		personal bool
	}
	tests := []struct {
		name     string
		readings []given
		bounds   []int
	}{
		{
			name:     "the password as typed, at the bound",
			readings: []given{{text: heavy("a"), once: false, personal: false}},
			bounds:   []int{maxL33tCost},
		},
		{
			name:     "the password as typed, past the bound",
			readings: []given{{text: over, once: false, personal: false}},
			bounds:   []int{0},
		},
		{
			name: "two asks as typed leave one, which a reading anyone is asked cannot take and the person's can",
			readings: []given{
				{text: heavy("a"), once: false, personal: false},
				{text: heavy("b"), once: false, personal: false},
				{text: heavy("c"), once: false, personal: true},
			},
			bounds: []int{maxL33tCost, 0, maxL33tCost},
		},
		{
			name: "a reading anyone is asked counts as two asks whatever the details",
			readings: []given{
				{text: heavy("a"), once: false, personal: false},
				{text: heavy("b"), once: false, personal: false},
				{text: heavy("c"), once: false, personal: false},
				{text: heavy("d"), once: false, personal: false},
			},
			bounds: []int{maxL33tCost, 0, 0, 0},
		},
		{
			name: "a reading past the bound takes nothing",
			readings: []given{
				{text: light("a"), once: false, personal: false},
				{text: over, once: false, personal: false},
				{text: light("b"), once: false, personal: true},
			},
			bounds: []int{maxL33tCost, 0, maxL33tCost},
		},
		{
			name: "light readings all fit",
			readings: []given{
				{text: "vexillumtrek", once: false, personal: false},
				{text: "vexillumtrex", once: false, personal: false},
				{text: "vexillumtrey", once: false, personal: true},
			},
			bounds: []int{maxL33tCost, maxL33tCost, maxL33tCost},
		},
		{
			name: "a spelling does not wait on the person's words",
			readings: []given{
				{text: heavy("a"), once: false, personal: false},
				{text: heavy("b"), once: false, personal: true},
				{text: light("c"), once: false, personal: false},
			},
			bounds: []int{maxL33tCost, 0, maxL33tCost},
		},
		{
			name: "the piece once leaves one ask for the person's words",
			readings: []given{
				{text: heavy("a"), once: false, personal: false},
				{text: light("b"), once: true, personal: false},
				{text: heavy("c"), once: false, personal: true},
			},
			bounds: []int{maxL33tCost, 0, maxL33tCost},
		},
		{
			name: "the piece once leaves that ask whoever the person is",
			readings: []given{
				{text: light("a"), once: false, personal: false},
				{text: heavy("b"), once: true, personal: false},
			},
			bounds: []int{maxL33tCost, 0},
		},
		{
			name: "the piece once fits beside that ask",
			readings: []given{
				{text: light("a"), once: false, personal: false},
				{text: light("b"), once: true, personal: false},
				{text: heavy("c"), once: false, personal: true},
			},
			bounds: []int{maxL33tCost, maxL33tCost, maxL33tCost},
		},
	}
	for _, tt := range tests {
		readings := make([]asked, 0, len(tt.readings))
		for _, r := range tt.readings {
			added := 0.0
			if r.once {
				added = 1
			}
			readings = append(readings, asked{text: r.text, added: added, bound: 0, personal: r.personal})
		}
		setBounds(readings)
		bounds := make([]int, 0, len(readings))
		for _, r := range readings {
			bounds = append(bounds, r.bound)
		}
		if !slices.Equal(bounds, tt.bounds) {
			t.Errorf("%s: setBounds gave the bounds %d, want %d", tt.name, bounds, tt.bounds)
		}
	}
}

func TestJudge_detailsNeverMakeAPasswordStronger(t *testing.T) {
	t.Parallel()

	const password = "S|lve$+riabababab|I+7137"
	own := Details{Name: "Tessa Silvestri", Email: "tessa@example.com"}
	other := Details{Name: "John Smith", Email: "john@example.com"}
	for _, minimum := range []int{44, 52} {
		without := Judge(password, Details{}, minimum)
		if got := Judge(password, other, minimum); got != without {
			t.Errorf("Judge(%q, %+v, %d) = %+v, want %+v as with no details", password, other, minimum, got, without)
		}
		if got := Judge(password, own, minimum); got.Strength > without.Strength {
			t.Errorf("Judge(%q, %+v, %d) = %+v, stronger than with no details %+v", password, own, minimum, got, without)
		}
	}
	if got := Judge(password, Details{}, 52); !got.StrongEnough {
		t.Fatalf("Judge(%q, no details, 52) = %+v, want it strong enough: the case proves nothing", password, got)
	}
	if got := Judge(password, own, 52); got.StrongEnough || got.Reason != ReasonPersonal {
		t.Errorf("Judge(%q, %+v, 52) = %+v, want it short for %q", password, own, got, ReasonPersonal)
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

func TestJudge_aPointAPersonMayLeaveOffChangesNothing(t *testing.T) {
	t.Parallel()

	who := Details{Name: "יצחק לוי"}
	for _, tt := range []struct{ pointed, plain string }{
		{pointed: "שָׁלוֹם", plain: "שלום"},
		{pointed: "יִצְחָק2077", plain: "יצחק2077"},
	} {
		if got, want := Judge(tt.pointed, Details{}, 30), Judge(tt.plain, Details{}, 30); got != want {
			t.Errorf("Judge(%q) = %+v, want %+v as for %q: a point is no character", tt.pointed, got, want, tt.plain)
		}
		got, want := Judge(tt.pointed, who, 30), Judge(tt.plain, who, 30)
		if got != want {
			t.Errorf("Judge(%q, %+v) = %+v, want %+v as for %q: a point is no character", tt.pointed, who, got, want, tt.plain)
		}
		if got.StrongEnough {
			t.Errorf("Judge(%q, %+v) = %+v, want it short", tt.pointed, who, got)
		}
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
