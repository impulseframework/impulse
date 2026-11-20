package accesstypes

import (
	"cmp"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
)

// PermissionMask is the attenuation an impersonated session carries. It says
// which permission kinds the session keeps (List, Read, Create, Update, Delete,
// Execute) and, inside a kept kind, what it refuses: a class of actions, or one
// permission on one named action or record. A mask can only narrow: a masked
// check first asks the mask and only then asks policy, so nothing a mask does
// can grant what policy denies.
//
// The mask is the control over what an impersonated session may do: a session
// that impersonates a person with no mask holds everything that person holds.
//
// The zero PermissionMask is unrestricted: every permission passes (AllowAll).
// A mask built with MaskPermissions keeps exactly the listed kinds, and DenyAll
// keeps none. Refusing adds what a mask refuses inside the kinds it keeps, so
// AllowAll().Refusing(RefuseClass(ClassCredential)) keeps everything except the
// credential actions. Text is the mask's persistence form and ParseMask reads it
// back.
type PermissionMask struct {
	// allowed are the kinds the mask keeps; nil keeps every kind.
	allowed map[Permission]struct{}
	// refusedClasses are the classes of action the mask refuses inside the kinds
	// it keeps.
	refusedClasses map[ActionClass]struct{}
	// refused are the named actions and records the mask refuses one permission on.
	refused map[namedRefusal]struct{}
}

// namedRefusal is one permission on one named resource.
type namedRefusal struct {
	permission Permission
	resource   Resource
}

// AllowAll returns the unrestricted mask: the zero value, spelled out.
func AllowAll() PermissionMask {
	return PermissionMask{}
}

// DenyAll returns the mask that allows nothing.
func DenyAll() PermissionMask {
	return PermissionMask{allowed: map[Permission]struct{}{}}
}

// MaskPermissions returns the mask keeping exactly the kinds perms. Duplicates
// and the NullPermission are ignored. With no permissions left it returns
// fallback, so the caller states what an empty list means where the list is
// built: AllowAll() for "no mask", DenyAll() for "nothing", or any narrower mask
// of its own. A list that may be empty cannot be spread into the function
// without that choice, which is the point.
func MaskPermissions(fallback PermissionMask, perms ...Permission) PermissionMask {
	allowed := make(map[Permission]struct{}, len(perms))
	for _, perm := range perms {
		if perm == NullPermission {
			continue
		}
		allowed[perm] = struct{}{}
	}
	if len(allowed) == 0 {
		return fallback
	}

	return PermissionMask{allowed: allowed}
}

// MaskRefusal is one thing a mask refuses inside the kinds it keeps: a class of
// actions (RefuseClass), or one permission on one named action or record
// (RefuseAction, RefusePermission, RefuseWrites).
type MaskRefusal struct {
	class ActionClass
	named namedRefusal
	// isClass says the refusal names a class; otherwise it names a resource.
	isClass bool
}

// RefuseClass refuses every action that carries class, whatever its kind.
func RefuseClass(class ActionClass) MaskRefusal {
	return MaskRefusal{class: class, isClass: true}
}

// RefuseAction refuses one action by its name: Execute on it.
func RefuseAction(action Resource) MaskRefusal {
	return RefusePermission(Execute, action)
}

// RefusePermission refuses one permission on one named action, record or field
// of a record (Record.field). A refusal that names a record covers its fields:
// Update refused on a record is refused on each of them. One that names a field
// refuses that field and leaves the record and its other fields alone.
func RefusePermission(perm Permission, res Resource) MaskRefusal {
	return MaskRefusal{named: namedRefusal{permission: perm, resource: res}}
}

// recordWrites are the three writes of a record.
var recordWrites = []Permission{Create, Update, Delete}

// RefuseWrites refuses the three writes of one record: Create, Update and Delete.
// A mask says the three in words as one, "writing" the record (Words).
func RefuseWrites(record Resource) []MaskRefusal {
	refusals := make([]MaskRefusal, len(recordWrites))
	for i, perm := range recordWrites {
		refusals[i] = RefusePermission(perm, record)
	}

	return refusals
}

// Class returns the class the refusal names and true, or the empty class and
// false for a refusal that names a resource.
func (r MaskRefusal) Class() (ActionClass, bool) {
	if !r.isClass {
		return "", false
	}

	return r.class, true
}

// Named returns the permission and the resource the refusal names and true, or
// zero values and false for a refusal that names a class.
func (r MaskRefusal) Named() (Permission, Resource, bool) {
	if r.isClass {
		return NullPermission, "", false
	}

	return r.named.permission, r.named.resource, true
}

// Refusing returns the mask with refusals added to what it refuses inside the
// kinds it keeps. The receiver is not changed.
func (m PermissionMask) Refusing(refusals ...MaskRefusal) PermissionMask {
	if len(refusals) == 0 {
		return m
	}

	out := PermissionMask{
		allowed:        m.allowed,
		refusedClasses: maps.Clone(m.refusedClasses),
		refused:        maps.Clone(m.refused),
	}
	for _, refusal := range refusals {
		if refusal.isClass {
			if out.refusedClasses == nil {
				out.refusedClasses = make(map[ActionClass]struct{}, len(refusals))
			}
			out.refusedClasses[refusal.class] = struct{}{}

			continue
		}
		if out.refused == nil {
			out.refused = make(map[namedRefusal]struct{}, len(refusals))
		}
		out.refused[refusal.named] = struct{}{}
	}

	return out
}

// Allows reports whether the mask keeps the kind perm. The unrestricted mask
// keeps every kind. It says nothing of what the mask refuses inside a kept kind:
// a check of one resource asks Refuses.
func (m PermissionMask) Allows(perm Permission) bool {
	if m.allowed == nil {
		return true
	}
	_, ok := m.allowed[perm]

	return ok
}

// Refuses reports whether the mask refuses perm on res, and why in a few words
// for the log: the mask does not keep the kind, it refuses one of the classes, or
// it refuses the permission on the resource by name. classes are the ones res
// carries: an action's, as the application's collection declares them, and none
// for a record, which carries no class. A field is refused with its record: res
// may be the dotted name of a field.
func (m PermissionMask) Refuses(perm Permission, res Resource, classes ...ActionClass) (why string, refused bool) {
	if !m.Allows(perm) {
		return fmt.Sprintf("the mask does not keep %s", perm), true
	}
	for _, class := range classes {
		if _, ok := m.refusedClasses[class]; ok {
			return fmt.Sprintf("the mask refuses the class %s", class), true
		}
	}
	if len(m.refused) > 0 {
		// The resource as it is named, which may be a field of a record, then the
		// record a field belongs to: a refusal that names a field refuses the field,
		// and one that names a record refuses the record and each of its fields.
		if _, ok := m.refused[namedRefusal{permission: perm, resource: res}]; ok {
			return fmt.Sprintf("the mask refuses %s on %s", perm, res), true
		}
		if base, _, isField := strings.Cut(string(res), "."); isField {
			if _, ok := m.refused[namedRefusal{permission: perm, resource: Resource(base)}]; ok {
				return fmt.Sprintf("the mask refuses %s on %s", perm, base), true
			}
		}
	}

	return "", false
}

// IsZero reports whether the mask is unrestricted: it keeps every kind and
// refuses nothing inside them.
func (m PermissionMask) IsZero() bool {
	return m.allowed == nil && len(m.refusedClasses) == 0 && len(m.refused) == 0
}

// KeepsEveryKind reports whether the mask keeps every permission kind. Such a
// mask may still refuse a class or a named resource (IsZero says it does not).
func (m PermissionMask) KeepsEveryKind() bool {
	return m.allowed == nil
}

// ReadOnly reports whether the mask is read-only: it names the kinds it keeps
// and keeps nothing beyond List and Read. The mask that keeps every kind is not
// read-only, whatever it refuses inside them; the mask that keeps nothing is.
func (m PermissionMask) ReadOnly() bool {
	if m.allowed == nil {
		return false
	}
	for perm := range m.allowed {
		if perm != List && perm != Read {
			return false
		}
	}

	return true
}

// Permissions returns the kinds the mask keeps, sorted, or nil for a mask that
// keeps every kind. A non-nil empty result is the mask that keeps nothing. It
// leaves out what the mask refuses inside those kinds (Refusals); Text is the
// whole mask.
func (m PermissionMask) Permissions() []Permission {
	if m.allowed == nil {
		return nil
	}

	perms := make([]Permission, 0, len(m.allowed))
	for perm := range m.allowed {
		perms = append(perms, perm)
	}
	slices.Sort(perms)

	return perms
}

// maskKinds are the permission kinds there are for a mask to keep.
var maskKinds = []Permission{Create, Delete, Execute, List, Read, Update}

// UnknownKinds returns the kinds the mask keeps that are no permission kind,
// sorted; none for a mask that keeps every kind. A kind spelled as none is
// ("read" for Read) keeps nothing, and whoever asked for it would believe
// otherwise: whatever takes a mask from a person refuses one that names such a
// kind, with the kinds there are (PermissionKinds).
func (m PermissionMask) UnknownKinds() []Permission {
	var unknown []Permission
	for perm := range m.allowed {
		if !slices.Contains(maskKinds, perm) {
			unknown = append(unknown, perm)
		}
	}
	slices.Sort(unknown)

	return unknown
}

// PermissionKinds returns the permission kinds there are for a mask to keep,
// sorted.
func PermissionKinds() []Permission {
	return slices.Clone(maskKinds)
}

// Refusals returns what the mask refuses inside the kinds it keeps: the classes
// first, sorted, then the named resources, sorted by resource and permission.
func (m PermissionMask) Refusals() []MaskRefusal {
	refusals := make([]MaskRefusal, 0, len(m.refusedClasses)+len(m.refused))
	for _, class := range slices.Sorted(maps.Keys(m.refusedClasses)) {
		refusals = append(refusals, RefuseClass(class))
	}
	named := slices.SortedFunc(maps.Keys(m.refused), func(a, b namedRefusal) int {
		return cmp.Or(cmp.Compare(a.resource, b.resource), cmp.Compare(a.permission, b.permission))
	})
	for _, refusal := range named {
		refusals = append(refusals, MaskRefusal{named: refusal})
	}

	return refusals
}

// Equal reports whether the two masks keep the same kinds and refuse the same
// things inside them.
func (m PermissionMask) Equal(other PermissionMask) bool {
	if (m.allowed == nil) != (other.allowed == nil) {
		return false
	}

	return maps.Equal(m.allowed, other.allowed) && maps.Equal(m.refusedClasses, other.refusedClasses) && maps.Equal(m.refused, other.refused)
}

// WiderMaskError is Narrow's refusal: the request keeps kinds the mask it
// narrows does not give.
type WiderMaskError struct {
	// Beyond are the kinds the request keeps and the narrowed mask does not, sorted.
	Beyond []Permission
	// Gives is the mask the request was held to.
	Gives PermissionMask
}

// Error says which kinds the request asked for beyond the mask, and what the
// mask gives.
func (e *WiderMaskError) Error() string {
	names := make([]string, len(e.Beyond))
	for i, perm := range e.Beyond {
		names[i] = string(perm)
	}

	return fmt.Sprintf("the request asks for %s, which is more than is given (%s)", joinWords(names), e.Gives.Words())
}

// Narrow returns the mask a request for a narrower one gets under m, the widest
// mask a grant gives. A request that names no kinds keeps the kinds m keeps; one
// that names kinds keeps those, each of which m must keep. What either refuses
// inside the kinds is refused: the refusals are joined. A request that keeps a
// kind m does not keep is refused with a *WiderMaskError, never trimmed, so
// nobody believes they hold what they do not. Neither mask is changed.
func (m PermissionMask) Narrow(request PermissionMask) (PermissionMask, error) {
	out := PermissionMask{allowed: maps.Clone(m.allowed)}
	if request.allowed != nil {
		var beyond []Permission
		for perm := range request.allowed {
			if !m.Allows(perm) {
				beyond = append(beyond, perm)
			}
		}
		if len(beyond) > 0 {
			slices.Sort(beyond)

			return PermissionMask{}, &WiderMaskError{Beyond: beyond, Gives: m}
		}
		out.allowed = maps.Clone(request.allowed)
	}

	return out.Refusing(m.Refusals()...).Refusing(request.Refusals()...), nil
}

// The marks of a mask's text: the kinds part of a mask that keeps every kind,
// what separates the parts, and what opens each kind of refusal.
const (
	maskEveryKind     = "*"
	maskPartSeparator = ";"
	maskClassPrefix   = "-class:"
	maskNamedPrefix   = "-on:"
)

// Text renders the mask in its persistence form: the kinds it keeps, sorted and
// joined with commas ("*" for every kind, nothing for none), then each refusal
// after a semicolon, a class as "-class:<class>" and a named resource as
// "-on:<permission>:<resource>". A read-only mask is "List,Read", and the mask
// that keeps everything except the credential actions is "*;-class:credential".
// Names are escaped, so any name survives the round trip through ParseMask. The
// unrestricted mask is "*": a store that keeps a NULL for it writes none.
func (m PermissionMask) Text() string {
	var text strings.Builder
	if m.allowed == nil {
		text.WriteString(maskEveryKind)
	}
	for i, perm := range m.Permissions() {
		if i > 0 {
			text.WriteByte(',')
		}
		text.WriteString(url.QueryEscape(string(perm)))
	}
	for _, refusal := range m.Refusals() {
		text.WriteString(maskPartSeparator)
		if class, ok := refusal.Class(); ok {
			text.WriteString(maskClassPrefix + url.QueryEscape(string(class)))

			continue
		}
		text.WriteString(maskNamedPrefix + url.QueryEscape(string(refusal.named.permission)) + ":" + url.QueryEscape(string(refusal.named.resource)))
	}

	return text.String()
}

// ParseMask reads a mask from its persistence form (Text). The empty text is the
// mask that keeps nothing. A text that is not a mask's is an error, and the
// caller holds the session to nothing: a mask is never read wider than it was
// written.
func ParseMask(text string) (PermissionMask, error) {
	parts := strings.Split(text, maskPartSeparator)

	mask := DenyAll()
	if kinds := strings.TrimSpace(parts[0]); kinds == maskEveryKind {
		mask = AllowAll()
	} else {
		for name := range strings.SplitSeq(kinds, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			perm, err := url.QueryUnescape(name)
			if err != nil {
				return PermissionMask{}, errors.Wrapf(err, "the mask %q names the kind %q, which does not read", text, name)
			}
			mask.allowed[Permission(perm)] = struct{}{}
		}
	}

	for _, part := range parts[1:] {
		refusal, err := parseMaskRefusal(strings.TrimSpace(part))
		if err != nil {
			return PermissionMask{}, errors.Wrapf(err, "the mask %q", text)
		}
		mask = mask.Refusing(refusal)
	}

	return mask, nil
}

// parseMaskRefusal reads one refusal of a mask's text.
func parseMaskRefusal(part string) (MaskRefusal, error) {
	if name, ok := strings.CutPrefix(part, maskClassPrefix); ok {
		class, err := url.QueryUnescape(name)
		if err != nil || class == "" {
			return MaskRefusal{}, errors.Newf("the refusal %q names no class", part)
		}

		return RefuseClass(ActionClass(class)), nil
	}
	if named, ok := strings.CutPrefix(part, maskNamedPrefix); ok {
		permName, resName, found := strings.Cut(named, ":")
		perm, permErr := url.QueryUnescape(permName)
		res, resErr := url.QueryUnescape(resName)
		if !found || permErr != nil || resErr != nil || perm == "" || res == "" {
			return MaskRefusal{}, errors.Newf("the refusal %q names no permission on a resource", part)
		}

		return RefusePermission(Permission(perm), Resource(res)), nil
	}

	return MaskRefusal{}, errors.Newf("the part %q is no refusal: one opens with %q or %q", part, maskClassPrefix, maskNamedPrefix)
}

// String renders the mask for evidence: "unrestricted" for the zero mask,
// otherwise its Text.
func (m PermissionMask) String() string {
	if m.IsZero() {
		return "unrestricted"
	}

	return m.Text()
}

// Words says the mask as a person would: "everything" for the unrestricted
// mask, "reading only" for the mask that keeps List and Read, "everything except
// credential actions" for the mask that refuses the credential class, "everything
// except writing Invoices" for the mask that refuses a record its three writes
// (RefuseWrites). It is what a list of impersonated sessions shows for each, and
// what a banner tells the person impersonating.
func (m PermissionMask) Words() string {
	var kept string
	switch perms := m.Permissions(); {
	case perms == nil:
		kept = "everything"
	case len(perms) == 0:
		return "nothing"
	case len(perms) == 2 && m.ReadOnly():
		kept = "reading only"
	default:
		names := make([]string, len(perms))
		for i, perm := range perms {
			names[i] = string(perm)
		}
		kept = joinWords(names) + " only"
	}

	refused := m.refusalWords()
	if len(refused) == 0 {
		return kept
	}
	if m.allowed == nil {
		return kept + " except " + joinWords(refused)
	}

	return kept + ", except " + joinWords(refused)
}

// refusalWords says what the mask refuses inside the kinds it keeps, each refusal as
// a person would, in the order of Refusals. A record refused its three writes
// (RefuseWrites) is said once, as "writing" it, where the first of the three stands:
// a mask that names several records' writes is read on a banner, and three parts a
// record would be three times as long for nothing more said. A record refused one or
// two of the three has each said.
func (m PermissionMask) refusalWords() []string {
	refusals := m.Refusals()
	words := make([]string, 0, len(refusals))
	said := make(map[Resource]bool)
	for _, refusal := range refusals {
		record := refusal.named.resource
		switch {
		case refusal.isClass || !m.refusesWrites(record) || !slices.Contains(recordWrites, refusal.named.permission):
			words = append(words, refusal.words())
		case !said[record]:
			said[record] = true
			words = append(words, "writing "+string(record))
		}
	}

	return words
}

// refusesWrites reports whether the mask names all three writes of the record.
func (m PermissionMask) refusesWrites(record Resource) bool {
	for _, perm := range recordWrites {
		if _, refused := m.refused[namedRefusal{permission: perm, resource: record}]; !refused {
			return false
		}
	}

	return true
}

// words says one refusal as a person would.
func (r MaskRefusal) words() string {
	if r.isClass {
		return string(r.class) + " actions"
	}
	switch r.named.permission {
	case Execute:
		return "the action " + string(r.named.resource)
	case Create:
		return "creating " + string(r.named.resource)
	case Update:
		return "changing " + string(r.named.resource)
	case Delete:
		return "deleting " + string(r.named.resource)
	case List:
		return "listing " + string(r.named.resource)
	case Read:
		return "reading " + string(r.named.resource)
	default:
		return string(r.named.permission) + " on " + string(r.named.resource)
	}
}

// joinWords joins words as a sentence lists them: "a", "a and b", "a, b and c".
func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
}
