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
//
// The words are for people. What two audiences are compared by is Identities, which
// names each organization by what is that organization's alone.
type Audience struct {
	// Organizations are the organizations the method is limited to, each as a person
	// would name it: a Google Workspace by its domain, a Microsoft Entra tenant by the
	// application's name for it.
	Organizations []string
	// Accounts is what an account the method admits outside those organizations is
	// called, with its article ("a Google account", "a Microsoft work or school
	// account", "an email address"); "" for a method limited to its organizations.
	Accounts string
	// Identities is whom the method admits as its provider identifies them, which is
	// what Key spells: an entry for each organization the method admits (a Microsoft
	// Entra tenant by its tenant id, a Google Workspace by its domain, another provider
	// by its issuer) and one for each kind of account it admits outside them, each
	// qualified by its kind so that no two kinds read alike. A name is the
	// application's to give, and a second organization can be given the first one's;
	// an identity is one organization's alone, so two registrations that admit
	// different people never share their identities, whatever they are called. The
	// library's own methods give them. A method that gives none is compared by the
	// names it gives (Organizations, Accounts), which then must be its organizations'
	// alone.
	Identities []string
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

// The kinds that qualify the identities of a method that gives none of its own: an
// organization by the name the method gives it, and the accounts it admits outside its
// organizations by what the method calls them.
const (
	identityNamedOrganization = "organization-named:"
	identityNamedAccounts     = "accounts-named:"
)

// audienceKey is an audience as its key spells it: the identities of whom the method
// admits, in order and each once.
type audienceKey struct {
	Admits []string `json:"admits"`
}

// Key is the audience in a form that compares exactly: two registrations admit the same
// people exactly when their keys are equal, whatever order their organizations were
// listed in and whatever they are called. It is what a sign-in method's row keeps of
// the audience the method was opened to sign-up for, so a registration that code
// changes afterwards is not open on the strength of a confirmation given for other
// people. The key spells the identities (Identities), never the names: a second
// organization listed under the name of the first changes the key, and the same
// organization given another name does not.
//
// A key reads back as its identities (IdentitiesOfKey). Text of any other form is no
// key and equals none, so a row that kept its audience in an earlier form, by its
// names, opens nothing.
func (a Audience) Key() string {
	admits := slices.Compact(slices.Sorted(slices.Values(a.identities())))
	if admits == nil {
		admits = []string{}
	}
	key, err := json.Marshal(audienceKey{Admits: admits})
	if err != nil {
		// A list of strings always marshals.
		panic("signin: an audience's key failed to marshal: " + err.Error())
	}

	return string(key)
}

// identities is whom the method admits, as the key spells them: the identities the
// method gives, or, for a method that gives none, its names under kinds of their own.
func (a Audience) identities() []string {
	if len(a.Identities) > 0 {
		return a.Identities
	}
	named := make([]string, 0, len(a.Organizations)+1)
	for _, organization := range a.Organizations {
		named = append(named, identityNamedOrganization+organization)
	}
	if a.Accounts != "" {
		named = append(named, identityNamedAccounts+a.Accounts)
	}

	return named
}

// IdentitiesOfKey reads back the identities a key was made of, in the key's order. ok
// is false for text that is no key as Key writes it, which names no audience: an
// earlier form of the key among them.
func IdentitiesOfKey(key string) (identities []string, ok bool) {
	var read audienceKey
	if err := json.Unmarshal([]byte(key), &read); err != nil {
		return nil, false
	}
	if (Audience{Identities: read.Admits}).Key() != key {
		return nil, false
	}

	return read.Admits, true
}

// orList joins names as a sentence lists alternatives: "a", "a or b", "a, b or c".
func orList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
