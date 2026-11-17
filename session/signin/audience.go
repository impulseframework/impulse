package signin

import "strings"

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

// orList joins names as a sentence lists alternatives: "a", "a or b", "a, b or c".
func orList(names []string) string {
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
