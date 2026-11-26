package auditlog

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/impulseframework/impulse/internal/words"
)

// The words a sentence of the log is written with where a row's parts give none.
const (
	// defaultTenantWord is what a tenant is called where the application names it nothing.
	defaultTenantWord = "tenant"
	// periodLayout writes a time inside a sentence. The server writes the sentence and does
	// not know the reader's time zone, so the time is in UTC and says so.
	periodLayout = "2006-01-02 15:04 UTC"
)

// Sentences is what a person reads of one row of the audit log.
type Sentences struct {
	Who    string // who or what acted
	What   string // the act in words
	DoneTo string // whom or what it was done to
}

// Sentences writes what a person reads of the row, from its parts. tenantWord is what the
// application calls a tenant ("" reads "tenant").
func (s *Stored) Sentences(tenantWord string) Sentences {
	return Sentences{Who: s.who(), What: s.what(tenantWord), DoneTo: s.doneTo()}
}

// who is who or what acted, as a person reads it: the directory, where one acted; the
// library's own process, where it acted with no person; and otherwise the sentence of
// who made a change that every record of the library writes (eventsource.Stored.String).
func (s *Stored) who() string {
	if s.Directory != "" {
		return "the directory " + s.Directory
	}
	if s.Source.ActorUserID == "" {
		if sentence, ok := processWords(s.Source.Process); ok {
			return sentence
		}
	}

	return s.Source.String()
}

// processWords is a process of the library's own that acts with no person behind it, in
// words, and false for any other. The directory sign-in process is not one: its row names
// the directory (who).
func processWords(process string) (string, bool) {
	switch process {
	case ProcessApplication:
		return "the application", true
	case ProcessMigrate:
		return "the migrate command", true
	case ProcessResetLink:
		return "a password reset link", true
	case ProcessAddressLink:
		return "an address confirmation link", true
	case ProcessInvitation:
		return "an invitation", true
	case ProcessSignUp:
		return "a sign-up", true
	case ProcessSignInPage:
		return "the sign-in page", true
	case ProcessDevice:
		return "a device the person approved", true
	case ProcessPeriodEnd:
		return "the end of its period", true
	default:
		return "", false
	}
}

// what is the act of the row in words, from its parts, by the sentence its act and form
// are written with (shapes). tenantWord is what the application calls a tenant ("" reads
// "tenant"). It never fails: an act it does not know is its stored name, and a part the
// sentence needs that the row lacks is written as nothing.
func (s *Stored) what(tenantWord string) string {
	shape, ok := shapeOf(s.Act, s.Form)
	if !ok {
		return string(s.Act)
	}
	parts := s.parts(tenantWord)
	sentence := shape.sentence
	if shape.ifEmpty != "" && parts[shape.ifEmpty] == "" {
		sentence = shape.otherwise
	}
	pairs := make([]string, 0, 2*len(parts))
	for _, part := range slices.Sorted(maps.Keys(parts)) {
		pairs = append(pairs, part, parts[part])
	}

	return strings.NewReplacer(pairs...).Replace(sentence)
}

// parts answers what fills each part in braces of a sentence, from the row. A value is
// written once and never read again for braces, so nothing a row holds can fill another
// part.
func (s *Stored) parts(tenantWord string) map[string]string {
	if tenantWord == "" {
		tenantWord = defaultTenantWord
	}

	return map[string]string{
		"{role}":              s.role(),
		"{roles}":             s.roles(),
		"{place}":             s.place(tenantWord),
		"{period}":            s.period(),
		"{tenant word}":       tenantWord,
		"{a tenant word}":     words.WithArticle(tenantWord),
		"{OldValue}":          s.OldValue,
		"{NewValue}":          s.NewValue,
		"{GroupName}":         s.GroupName,
		"{SeatingTenantName}": s.SeatingTenantName,
		"{SubjectName}":       s.SubjectName,
		"{length}":            rememberedLength(s.NewValue),
		"{to tenant}":         s.toTenant(),
	}
}

// toTenant is the tenant the row names, as a sentence ends with it: " to" and the
// tenant's name, nothing where the row names no tenant.
func (s *Stored) toTenant() string {
	if s.TenantName == "" {
		return ""
	}

	return " to " + s.TenantName
}

// role is the first role the row names, in words.
func (s *Stored) role() string {
	if len(s.RoleNames) == 0 {
		return ""
	}

	return words.RoleInWords(s.RoleNames[0])
}

// roles is every role the row names, in words and in order, after "role" for one and
// "roles" for more: "role Editor", "roles Editor, Night shift lead".
func (s *Stored) roles() string {
	if len(s.RoleNames) == 0 {
		return ""
	}
	spelled := make([]string, 0, len(s.RoleNames))
	for _, role := range s.RoleNames {
		spelled = append(spelled, words.RoleInWords(role))
	}
	if len(spelled) == 1 {
		return "role " + spelled[0]
	}

	return "roles " + strings.Join(spelled, ", ")
}

// place is where the row's role is held, as a sentence ends with it: nothing for one
// tenant, which the row's tenant columns name, " for the whole application", or " in
// every" and the tenant word.
func (s *Stored) place(tenantWord string) string {
	switch s.RoleScope {
	case RoleScopeGlobal:
		return " for the whole application"
	case RoleScopeEveryTenant:
		return " in every " + tenantWord
	case RoleScopeTenant:
		return ""
	default:
		return ""
	}
}

// period is the period of the role or the place the row gives, as a sentence ends with
// it: nothing with neither time, " from" its start, " until" its end, or both.
func (s *Stored) period() string {
	var period string
	if !s.PeriodStart.IsZero() {
		period += " from " + s.PeriodStart.UTC().Format(periodLayout)
	}
	if !s.PeriodEnd.IsZero() {
		period += " until " + s.PeriodEnd.UTC().Format(periodLayout)
	}

	return period
}

// doneTo is whom or what the act of the row was done to, as a person reads it: the
// subject's name when the act happened (its id where the row keeps no name), and a
// setting in words.
func (s *Stored) doneTo() string {
	if s.SubjectKind != SubjectSetting {
		if s.SubjectName == "" {
			return s.SubjectID
		}

		return s.SubjectName
	}
	switch {
	case strings.HasPrefix(s.SubjectID, settingSignUpPrefix):
		return "Sign-up through " + s.SubjectName
	case s.SubjectID == SettingSecondFactor:
		return "Second factor"
	case s.SubjectID == SettingRememberedBrowsers:
		return "Remembered browsers"
	case s.SubjectID == SettingSelfServiceReset:
		return "Password reset at the sign-in page"
	default:
		return s.SubjectID
	}
}

// rememberedLength is how long a browser is remembered, in the words the list of lengths
// gives it, from its days in text: "Not remembered" for 0, "1 day", and "{n} days". A
// text that is not a whole number is written as it is.
func rememberedLength(days string) string {
	n, err := strconv.Atoi(days)
	switch {
	case err != nil:
		return days
	case n == 0:
		return "Not remembered"
	case n == 1:
		return "1 day"
	default:
		return strconv.Itoa(n) + " days"
	}
}
