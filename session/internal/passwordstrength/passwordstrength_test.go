package passwordstrength

import (
	"strings"
	"testing"
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
			if !tt.strongEnough && got.Reason != ReasonGuessable {
				t.Errorf("Judge(%q, 30).Reason = %q, want %q", tt.password, got.Reason, ReasonGuessable)
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

func TestJudge_weakWithOtherDetailsStaysGuessable(t *testing.T) {
	t.Parallel()

	who := Details{Username: "someoneelse", Name: "Other Person", Email: "other@example.com"}
	got := Judge("rosebud", who, 30)
	if got.StrongEnough || got.Reason != ReasonGuessable {
		t.Errorf("Judge(rosebud, %+v) = %+v, want it short for %q", who, got, ReasonGuessable)
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
		{"", ""},
	}
	for _, tt := range tests {
		if got := tt.reason.Sentence(); got != tt.want {
			t.Errorf("Reason(%q).Sentence() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}
