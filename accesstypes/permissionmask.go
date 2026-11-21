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
// credential actions. A refusal of a class may carry exceptions (RefuseClassExcept):
// a record's writes, an action or a field the session keeps while the rest of the
// class stays refused, whatever is added to the class later. Text is the mask's
// persistence form and ParseMask reads it back.
type PermissionMask struct {
	// allowed are the kinds the mask keeps; nil keeps every kind.
	allowed map[Permission]struct{}
	// refusedClasses are the classes of action the mask refuses inside the kinds
	// it keeps.
	refusedClasses map[ActionClass]struct{}
	// refused are the named actions and records the mask refuses one permission on.
	refused map[namedRefusal]struct{}
	// excepted are the exceptions of the refused classes: what each class's refusal
	// leaves alone. Every entry's class is one of refusedClasses.
	excepted map[classException]struct{}
}

// namedRefusal is one permission on one named resource.
type namedRefusal struct {
	permission Permission
	resource   Resource
}

// classException is one permission on one named resource that the refusal of a class
// leaves alone.
type classException struct {
	class ActionClass
	named namedRefusal
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
// actions (RefuseClass), a class with exceptions (RefuseClassExcept), or one
// permission on one named action or record (RefuseAction, RefusePermission,
// RefuseWrites).
type MaskRefusal struct {
	class ActionClass
	named namedRefusal
	// isClass says the refusal names a class; otherwise it names a resource.
	isClass bool
	// exceptions are what a refusal of a class leaves alone, sorted by resource and
	// permission, each once; none for a refusal that names a resource.
	exceptions []namedRefusal
}

// RefuseClass refuses every action that carries class, whatever its kind.
func RefuseClass(class ActionClass) MaskRefusal {
	return MaskRefusal{class: class, isClass: true}
}

// MaskException is one thing the refusal of a class leaves alone: one permission on
// one named action, record or field (ExceptAction, ExceptPermission, ExceptWrites).
type MaskException struct {
	named namedRefusal
}

// ExceptAction keeps one action out of a class's refusal by its name: Execute on it.
func ExceptAction(action Resource) MaskException {
	return ExceptPermission(Execute, action)
}

// ExceptPermission keeps one permission on one named action, record or field of a
// record (Record.field) out of a class's refusal. An exception that names a record
// covers its fields. One that names a field keeps a write that sets that field and
// no field outside the exceptions (PermissionMask.RefusesWrite): the record's other
// fields stay refused with the class.
func ExceptPermission(perm Permission, res Resource) MaskException {
	return MaskException{named: namedRefusal{permission: perm, resource: res}}
}

// ExceptWrites keeps the three writes of one record out of a class's refusal:
// Create, Update and Delete. A mask says the three in words as one, "writing" the
// record (Words).
func ExceptWrites(record Resource) []MaskException {
	exceptions := make([]MaskException, len(recordWrites))
	for i, perm := range recordWrites {
		exceptions[i] = ExceptPermission(perm, record)
	}

	return exceptions
}

// Named returns the permission and the resource the exception names.
func (e MaskException) Named() (Permission, Resource) {
	return e.named.permission, e.named.resource
}

// Words says the exception as a person would: "the action AddGroupMember",
// "changing Pilots".
func (e MaskException) Words() string {
	return e.named.words()
}

// RefuseClassExcept refuses every action that carries class except the ones named:
// a name beats its class, and everything else in the class stays refused, whatever
// is added to it later. The exceptions belong to this refusal of this class: another
// class the same name carries, refused by the same mask, still refuses it.
//
//	accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptWrites("HangarPasses")...)
func RefuseClassExcept(class ActionClass, exceptions ...MaskException) MaskRefusal {
	refusal := MaskRefusal{class: class, isClass: true}
	for _, exception := range exceptions {
		if !slices.Contains(refusal.exceptions, exception.named) {
			refusal.exceptions = append(refusal.exceptions, exception.named)
		}
	}
	slices.SortFunc(refusal.exceptions, compareNamed)

	return refusal
}

// compareNamed orders named permissions by resource, then permission.
func compareNamed(a, b namedRefusal) int {
	return cmp.Or(cmp.Compare(a.resource, b.resource), cmp.Compare(a.permission, b.permission))
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

// Exceptions returns what a refusal of a class leaves alone, sorted by resource and
// permission; none for a refusal that carries no exception or names a resource.
func (r MaskRefusal) Exceptions() []MaskException {
	exceptions := make([]MaskException, len(r.exceptions))
	for i, named := range r.exceptions {
		exceptions[i] = MaskException{named: named}
	}

	return exceptions
}

// Refusing returns the mask with refusals added to what it refuses inside the
// kinds it keeps. The receiver is not changed. Adding can only refuse more: a class
// the mask already refuses keeps the exceptions both refusals leave alone, so a
// class refused again with fewer exceptions loses the others, and one refused again
// with none loses them all. A class the mask did not refuse takes the exceptions of
// the refusal that adds it.
func (m PermissionMask) Refusing(refusals ...MaskRefusal) PermissionMask {
	if len(refusals) == 0 {
		return m
	}

	out := PermissionMask{
		allowed:        m.allowed,
		refusedClasses: maps.Clone(m.refusedClasses),
		refused:        maps.Clone(m.refused),
		excepted:       maps.Clone(m.excepted),
	}
	for _, refusal := range refusals {
		if refusal.isClass {
			out.refuseClass(refusal)

			continue
		}
		if out.refused == nil {
			out.refused = make(map[namedRefusal]struct{}, len(refusals))
		}
		out.refused[refusal.named] = struct{}{}
	}

	return out
}

// refuseClass adds the refusal of a class to the mask, whose maps are its own. A
// class refused for the first time takes the refusal's exceptions. One refused
// already keeps what both leave alone: each exception of either that the other
// covers, so a field one side keeps under a record the other keeps stays as the
// field.
func (m *PermissionMask) refuseClass(refusal MaskRefusal) {
	_, already := m.refusedClasses[refusal.class]
	if m.refusedClasses == nil {
		m.refusedClasses = make(map[ActionClass]struct{})
	}
	m.refusedClasses[refusal.class] = struct{}{}

	kept := make(map[namedRefusal]struct{}, len(refusal.exceptions))
	for _, named := range refusal.exceptions {
		if !already || m.excepts(refusal.class, named.permission, named.resource) {
			kept[named] = struct{}{}
		}
	}
	for exception := range m.excepted {
		if exception.class != refusal.class {
			continue
		}
		if refusal.covers(exception.named) {
			kept[exception.named] = struct{}{}
		}
		delete(m.excepted, exception)
	}
	if len(kept) > 0 && m.excepted == nil {
		m.excepted = make(map[classException]struct{}, len(kept))
	}
	for named := range kept {
		m.excepted[classException{class: refusal.class, named: named}] = struct{}{}
	}
}

// covers reports whether the refusal's exceptions leave the named permission alone:
// one names it, or it is on a field of a record one names.
func (r MaskRefusal) covers(named namedRefusal) bool {
	if slices.Contains(r.exceptions, named) {
		return true
	}
	record, _, isField := strings.Cut(string(named.resource), ".")

	return isField && slices.Contains(r.exceptions, namedRefusal{permission: named.permission, resource: Resource(record)})
}

// excepts reports whether the mask's refusal of class leaves perm on res alone: an
// exception names it, or res is a field of a record an exception names.
func (m PermissionMask) excepts(class ActionClass, perm Permission, res Resource) bool {
	if len(m.excepted) == 0 {
		return false
	}
	if _, ok := m.excepted[classException{class: class, named: namedRefusal{permission: perm, resource: res}}]; ok {
		return true
	}
	record, _, isField := strings.Cut(string(res), ".")
	if !isField {
		return false
	}
	_, ok := m.excepted[classException{class: class, named: namedRefusal{permission: perm, resource: Resource(record)}}]

	return ok
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
// it refuses the permission on the resource by name. classes are the ones perm on
// res carries, as the application's collection declares them: an action's, and
// for a record the ones its write carries, which the generator gives the writes
// of a record that decides what a person may do. A field is refused with its
// record: res may be the dotted name of a field.
//
// A name beats its class: a class the mask refuses does not refuse what one of its
// exceptions names (RefuseClassExcept), a record's exception covering its fields. A
// refusal by name still does, and so does another refused class the name carries.
// A record whose exceptions name only fields is refused here, since the question
// names no field: a write of it asks RefusesWrite.
func (m PermissionMask) Refuses(perm Permission, res Resource, classes ...ActionClass) (why string, refused bool) {
	if !m.Allows(perm) {
		return fmt.Sprintf("the mask does not keep %s", perm), true
	}
	for _, class := range classes {
		if _, ok := m.refusedClasses[class]; ok && !m.excepts(class, perm, res) {
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

// RefusesWrite is Refuses for a write of a record that sets fields, each named as a
// check names a field (Record.field). It differs for a record a refused class covers
// and whose exceptions name fields and not the record: the write is kept when it sets
// at least one field and every field it sets is one of the exceptions, for each
// refused class the record's write carries. A write that sets any other field, or
// none, is refused with the class, so an exception of a field keeps that field and
// nothing else of the record. Everything else is as Refuses says.
func (m PermissionMask) RefusesWrite(perm Permission, record Resource, fields []Resource, classes ...ActionClass) (why string, refused bool) {
	why, refused = m.Refuses(perm, record, classes...)
	if !refused || len(fields) == 0 || !m.Allows(perm) {
		return why, refused
	}
	for _, class := range classes {
		if _, ok := m.refusedClasses[class]; !ok || m.excepts(class, perm, record) {
			continue
		}
		for _, field := range fields {
			if !m.excepts(class, perm, field) {
				return fmt.Sprintf("the mask refuses the class %s, and its exceptions do not keep %s on %s", class, perm, field), true
			}
		}
	}
	if _, ok := m.refused[namedRefusal{permission: perm, resource: record}]; ok {
		return fmt.Sprintf("the mask refuses %s on %s", perm, record), true
	}

	return "", false
}

// ExceptedFields returns the fields of record that the mask's exceptions keep perm
// on, sorted, each named as a check names a field (Record.field); none for a mask
// whose exceptions name no field of the record.
func (m PermissionMask) ExceptedFields(perm Permission, record Resource) []Resource {
	var fields []Resource
	for exception := range m.excepted {
		if exception.named.permission != perm {
			continue
		}
		base, _, isField := strings.Cut(string(exception.named.resource), ".")
		if isField && Resource(base) == record && !slices.Contains(fields, exception.named.resource) {
			fields = append(fields, exception.named.resource)
		}
	}
	slices.Sort(fields)

	return fields
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
// first, sorted, each with its exceptions, then the named resources, sorted by
// resource and permission.
func (m PermissionMask) Refusals() []MaskRefusal {
	refusals := make([]MaskRefusal, 0, len(m.refusedClasses)+len(m.refused))
	for _, class := range slices.Sorted(maps.Keys(m.refusedClasses)) {
		refusal := RefuseClass(class)
		for exception := range m.excepted {
			if exception.class == class {
				refusal.exceptions = append(refusal.exceptions, exception.named)
			}
		}
		slices.SortFunc(refusal.exceptions, compareNamed)
		refusals = append(refusals, refusal)
	}
	for _, refusal := range slices.SortedFunc(maps.Keys(m.refused), compareNamed) {
		refusals = append(refusals, MaskRefusal{named: refusal})
	}

	return refusals
}

// Equal reports whether the two masks keep the same kinds and refuse the same
// things inside them, with the same exceptions.
func (m PermissionMask) Equal(other PermissionMask) bool {
	if (m.allowed == nil) != (other.allowed == nil) {
		return false
	}

	return maps.Equal(m.allowed, other.allowed) && maps.Equal(m.refusedClasses, other.refusedClasses) &&
		maps.Equal(m.refused, other.refused) && maps.Equal(m.excepted, other.excepted)
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

// AddedExceptionError is Narrow's refusal of a request that keeps out of a class's
// refusal what the mask it narrows does not: an exception the grant lacks.
type AddedExceptionError struct {
	// Class is the class both masks refuse.
	Class ActionClass
	// Added are the exceptions the request names and the narrowed mask does not
	// carry, sorted by resource and permission.
	Added []MaskException
	// Gives is the mask the request was held to.
	Gives PermissionMask
}

// Error says which exceptions the request added, and what the mask gives.
func (e *AddedExceptionError) Error() string {
	return fmt.Sprintf(
		"the request keeps %s out of the refusal of %s actions, which is more than is given (%s)",
		e.AddedWords(), e.Class, e.Gives.Words(),
	)
}

// AddedWords says the added exceptions as a person would, a record's three writes as
// one: "writing Pilots and the action AddGroupMember".
func (e *AddedExceptionError) AddedWords() string {
	named := make([]namedRefusal, len(e.Added))
	for i, exception := range e.Added {
		named[i] = exception.named
	}

	return joinWords(namedWords(named))
}

// Narrow returns the mask a request for a narrower one gets under m, the widest
// mask a grant gives. A request that names no kinds keeps the kinds m keeps; one
// that names kinds keeps those, each of which m must keep. What either refuses
// inside the kinds is refused: the refusals are joined. A request that keeps a
// kind m does not keep is refused with a *WiderMaskError, never trimmed, so
// nobody believes they hold what they do not. Neither mask is changed.
//
// A class m refuses with exceptions stays as m has it when the request does not
// refuse the class. A request that refuses the class too says which of m's
// exceptions it keeps: it may drop one, or all with a plain refusal of the class,
// and never add one m lacks, which is refused with an *AddedExceptionError. What
// the request refuses by name is refused whatever the exceptions say, and an
// exception so refused is dropped from the mask, so its words never say that
// something is both kept and refused.
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

	for _, refusal := range request.Refusals() {
		if _, both := m.refusedClasses[refusal.class]; !refusal.isClass || !both {
			continue
		}
		var added []MaskException
		for _, named := range refusal.exceptions {
			if !m.excepts(refusal.class, named.permission, named.resource) {
				added = append(added, MaskException{named: named})
			}
		}
		if len(added) > 0 {
			return PermissionMask{}, &AddedExceptionError{Class: refusal.class, Added: added, Gives: m}
		}
	}

	out = out.Refusing(m.Refusals()...).Refusing(request.Refusals()...)
	for exception := range out.excepted {
		if _, refused := out.Refuses(exception.named.permission, exception.named.resource); refused {
			delete(out.excepted, exception)
		}
	}

	return out, nil
}

// The marks of a mask's text: the kinds part of a mask that keeps every kind,
// what separates the parts, and what opens each kind of refusal.
const (
	maskEveryKind     = "*"
	maskPartSeparator = ";"
	maskClassPrefix   = "-class:"
	maskNamedPrefix   = "-on:"
	maskExceptPrefix  = "+class:"
)

// Text renders the mask in its persistence form: the kinds it keeps, sorted and
// joined with commas ("*" for every kind, nothing for none), then each refusal
// after a semicolon, a class as "-class:<class>" and a named resource as
// "-on:<permission>:<resource>". An exception follows its class as
// "+class:<class>:<permission>:<resource>". A read-only mask is "List,Read", and
// the mask that keeps everything except the credential actions is
// "*;-class:credential". Names are escaped, so any name survives the round trip
// through ParseMask. The unrestricted mask is "*": a store that keeps a NULL for it
// writes none.
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
			for _, named := range refusal.exceptions {
				text.WriteString(maskPartSeparator + maskExceptPrefix + url.QueryEscape(string(class)) + ":" +
					url.QueryEscape(string(named.permission)) + ":" + url.QueryEscape(string(named.resource)))
			}

			continue
		}
		text.WriteString(maskNamedPrefix + url.QueryEscape(string(refusal.named.permission)) + ":" + url.QueryEscape(string(refusal.named.resource)))
	}

	return text.String()
}

// ParseMask reads a mask from its persistence form (Text). The empty text is the
// mask that keeps nothing. A text that is not a mask's is an error, and the
// caller holds the session to nothing: a mask is never read wider than it was
// written. An exception of a class the text does not refuse is such a text.
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

	exceptions := make(map[ActionClass][]MaskException)
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, maskExceptPrefix) {
			class, exception, err := parseMaskException(part)
			if err != nil {
				return PermissionMask{}, errors.Wrapf(err, "the mask %q", text)
			}
			exceptions[class] = append(exceptions[class], exception)

			continue
		}
		refusal, err := parseMaskRefusal(part)
		if err != nil {
			return PermissionMask{}, errors.Wrapf(err, "the mask %q", text)
		}
		mask = mask.Refusing(refusal)
	}
	for _, class := range slices.Sorted(maps.Keys(exceptions)) {
		if _, refused := mask.refusedClasses[class]; !refused {
			return PermissionMask{}, errors.Newf("the mask %q keeps something out of the refusal of the class %q, which it does not refuse", text, string(class))
		}
		// The class is refused with no exception so far, and refusing it again would
		// keep only what both leave alone, so the exceptions are set where they stand.
		if mask.excepted == nil {
			mask.excepted = make(map[classException]struct{})
		}
		for _, exception := range exceptions[class] {
			mask.excepted[classException{class: class, named: exception.named}] = struct{}{}
		}
	}

	return mask, nil
}

// parseMaskException reads one exception of a mask's text: the class it belongs to,
// and the permission and the resource the class's refusal leaves alone.
func parseMaskException(part string) (ActionClass, MaskException, error) {
	names := strings.Split(strings.TrimPrefix(part, maskExceptPrefix), ":")
	if len(names) != 3 {
		return "", MaskException{}, errors.Newf("the exception %q names no class, permission and resource", part)
	}
	class, classErr := url.QueryUnescape(names[0])
	perm, permErr := url.QueryUnescape(names[1])
	res, resErr := url.QueryUnescape(names[2])
	if classErr != nil || permErr != nil || resErr != nil || class == "" || perm == "" || res == "" {
		return "", MaskException{}, errors.Newf("the exception %q names no class, permission and resource", part)
	}

	return ActionClass(class), ExceptPermission(Permission(perm), Resource(res)), nil
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

	return MaskRefusal{}, errors.Newf("the part %q is no refusal: one opens with %q or %q, and an exception with %q", part, maskClassPrefix, maskNamedPrefix, maskExceptPrefix)
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
// (RefuseWrites), and "everything except access actions (still allowed: writing
// Passes)" for the mask whose refusal of a class carries an exception
// (RefuseClassExcept). It is what a list of impersonated sessions shows for each,
// and what a banner tells the person impersonating.
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
	var named []namedRefusal
	for _, refusal := range refusals {
		if refusal.isClass {
			words = append(words, refusal.words())

			continue
		}
		named = append(named, refusal.named)
	}

	return append(words, namedWords(named)...)
}

// namedWords says named permissions as a person would, in their order, a record
// named with its three writes once, as "writing" it, where the first of the three
// stands.
func namedWords(named []namedRefusal) []string {
	writes := func(record Resource) bool {
		for _, perm := range recordWrites {
			if !slices.Contains(named, namedRefusal{permission: perm, resource: record}) {
				return false
			}
		}

		return true
	}

	words := make([]string, 0, len(named))
	said := make(map[Resource]bool)
	for _, one := range named {
		switch {
		case !slices.Contains(recordWrites, one.permission) || !writes(one.resource):
			words = append(words, one.words())
		case !said[one.resource]:
			said[one.resource] = true
			words = append(words, "writing "+string(one.resource))
		}
	}

	return words
}

// words says one refusal as a person would. A class with exceptions says them after
// it: "access actions (still allowed: writing Passes)".
func (r MaskRefusal) words() string {
	if !r.isClass {
		return r.named.words()
	}
	if len(r.exceptions) == 0 {
		return string(r.class) + " actions"
	}

	return string(r.class) + " actions (still allowed: " + joinWords(namedWords(r.exceptions)) + ")"
}

// words says one permission on one named resource as a person would.
func (n namedRefusal) words() string {
	switch n.permission {
	case Execute:
		return "the action " + string(n.resource)
	case Create:
		return "creating " + string(n.resource)
	case Update:
		return "changing " + string(n.resource)
	case Delete:
		return "deleting " + string(n.resource)
	case List:
		return "listing " + string(n.resource)
	case Read:
		return "reading " + string(n.resource)
	default:
		return string(n.permission) + " on " + string(n.resource)
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
