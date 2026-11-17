package signin

import (
	"encoding/json"
	"slices"
	"strings"
)

// Audience is how wide a sign-in method is: the people its provider could sign in,
// which the method's registration fixes in code. A method limited to organizations it
// names admits their people alone; a method open to any organization, or to personal
// accounts, admits anyone who has such an account. It is what opening the method to
// sign-up admits, so the page where an administrator opens a method says it in words
// (Words), and the warning the administrator confirms says it too.
type Audience struct {
	// Organizations are the organizations the method is limited to, each as a person
	// would name it: a Google Workspace by its domain, a Microsoft Entra tenant by the
	// application's name for it.
	Organizations []string
	// Accounts is what an account the method admits outside those organizations is
	// called, with its article ("a Google account", "a Microsoft work or school
	// account", "an email address"); "" for a method limited to its organizations.
	Accounts string
}

// Limited reports whether the method admits the people of the organizations it names
// and nobody else.
func (a Audience) Limited() bool {
	return a.Accounts == "" && len(a.Organizations) > 0
}

// Words says whom the method admits, as the middle of a sentence: "anyone with an
// account at acme.example" for a method limited to its organizations, "anyone with a
// Google account" for one that is not. A method that says nothing about itself admits
// "anyone who can sign in through it".
func (a Audience) Words() string {
	switch {
	case a.Accounts != "":
		return "anyone with " + a.Accounts
	case len(a.Organizations) > 0:
		return "anyone with an account at " + orList(a.Organizations)
	default:
		return "anyone who can sign in through it"
	}
}

// audienceKey is an audience as its key spells it: the two facts, the organizations in
// order and each once.
type audienceKey struct {
	Organizations []string `json:"organizations"`
	Accounts      string   `json:"accounts"`
}

// Key is the audience in a form that compares exactly: two registrations admit the same
// people exactly when their keys are equal, whatever order their organizations were
// named in. It is what a sign-in method's row keeps of the audience the method was
// opened to sign-up for, so a registration that is widened in code afterwards is not
// open on the strength of a confirmation that named a narrower audience. The audience
// reads back from its key (AudienceOfKey), to be said in words.
func (a Audience) Key() string {
	organizations := slices.Compact(slices.Sorted(slices.Values(a.Organizations)))
	if organizations == nil {
		organizations = []string{}
	}
	key, err := json.Marshal(audienceKey{Organizations: organizations, Accounts: a.Accounts})
	if err != nil {
		// Two strings and a list of strings always marshal.
		panic("signin: an audience's key failed to marshal: " + err.Error())
	}

	return string(key)
}

// AudienceOfKey reads back the audience a key was made of. ok is false for text that is
// no key, which names no audience.
func AudienceOfKey(key string) (audience Audience, ok bool) {
	var read audienceKey
	if err := json.Unmarshal([]byte(key), &read); err != nil {
		return Audience{}, false
	}
	audience = Audience{Organizations: read.Organizations, Accounts: read.Accounts}
	if audience.Key() != key {
		return Audience{}, false
	}

	return audience, true
}

// orList joins names as a sentence lists alternatives: "a", "a or b", "a, b or c".
func orList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
