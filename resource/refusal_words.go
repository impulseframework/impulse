package resource

import (
	"fmt"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
)

// This file holds the words a caller is told when a permission refuses what was asked.
// Every such refusal is one sentence a person can read: the permission by its verb
// (permissionVerb), the resource by its name in words, a field denied alone by its own
// name in words. No sentence carries an id, a permission's code name or a table's name.
// Those stay in the refusal's cause, which the server's log prints with the scope and
// the person, as it did when they were the sentence.

// refusedPlainly is what a caller is told when what was refused has no plain words: a
// permission permissionVerb has no verb for (an action an application declares, a
// method's Execute), or nothing that is named.
const refusedPlainly = "You may not do this here."

// The places a refusal speaks of: the tenant the request was made in, and every tenant a
// list across tenants reads.
const (
	refusedHere     = "here"
	refusedAnywhere = "anywhere"
)

// The ways a list is asked for by a field, as a refusal says them.
const (
	bySortOrFilter = "sort or filter"
	bySearch       = "search"
)

// permissionVerb is the library's one table of the verb a refusal says for a permission.
// A permission it holds no verb for answers "", and its refusal is refusedPlainly.
func permissionVerb(perm accesstypes.Permission) string {
	switch perm {
	case accesstypes.Create:
		return "add"
	case accesstypes.Read:
		return "see"
	case accesstypes.Update:
		return "change"
	case accesstypes.Delete:
		return "delete"
	case accesstypes.List:
		return "list"
	default:
		return ""
	}
}

// NotPermitted is the answer to a caller who does not hold perm on the resources denied:
// Forbidden, with one sentence a person can read. A resource is said by its name in
// words ("You may not change hangar passes here."), a field denied alone by its own
// ("You may not change the budget of hangar passes here."), and a permission with no
// plain verb, a method's Execute among them, names nothing ("You may not do this
// here."). The cause says who was refused what, by the scope, the person, the
// permission's code name and the resources' own names; it is kept for the server's log
// and no word of it reaches the caller. A handler that checks a permission itself
// answers its refusal with NotPermitted, so every refusal of an application reads the
// same. It answers the type httpio's own refusals do, and is returned as one of those
// is.
func NotPermitted(cause error, perm accesstypes.Permission, denied ...accesstypes.Resource) errors.Chain {
	return httpio.NewForbiddenMessageWithError(cause, mayNot(perm, denied, refusedHere))
}

// methodRefused is the answer to a caller who may not run a method. A method is no thing
// a person knows by a word, so the sentence names nothing; the cause, with the method's
// name, is kept for the server's log.
func methodRefused(cause error) error {
	return httpio.NewForbiddenMessageWithError(cause, refusedPlainly)
}

// TargetRefused is the answer to a caller whose method may not run against the row it
// names: Forbidden, with one sentence a person can read. The sentence says the row by
// its record's name in words and nothing else ("You may not do this to this mission."):
// no method's name and no key, and the same words whether the row's state or the
// grant's condition refused, so the caller is never told which. A record with no name
// names nothing ("You may not do this here."). The cause says the method by its name,
// the record by its struct's name and the row by its key's values in order, or "this"
// record where the row is one the method proposes and has no key; it is kept for the
// server's log and no word of it reaches the caller. A generated method's frame answers
// its state check with TargetRefused and its gate (ExecuteGate) answers the grant's
// condition with it, so both read the same. It answers the type httpio's own refusals
// do, and is returned as one of those is.
func TargetRefused(method accesstypes.Resource, label string, key ...any) errors.Chain {
	return httpio.NewForbiddenMessageWithError(targetRefusedCause(method, label, key), mayNotDoThisTo(label))
}

// targetRefusedCause is what the server's log keeps of a method refused on a row: the
// method, the record's struct name and the row's key, in the words the refusal itself
// said before it was put in a person's.
func targetRefusedCause(method accesstypes.Resource, label string, key []any) error {
	if len(key) == 0 {
		return errors.Newf("%s may not run against this %s", method, label)
	}
	values := make([]string, 0, len(key))
	for _, value := range key {
		values = append(values, fmt.Sprint(value))
	}

	return errors.Newf("%s may not run against %s %s", method, label, strings.Join(values, ", "))
}

// TargetNotFound is the answer to a caller who names a row that is not there for it:
// NotFound, with one sentence a person can read. The sentence says the row by its
// record's name in words and nothing else ("This mission does not exist."): no key, and
// the same words whether no row has the key, the row is another tenant's or the
// caller's grant does not admit it, so the caller is never told that a row is there. A
// record with no name is "this record". The cause says the record by its struct's name
// and the row by its key's values in order, or "this" record where there is no key to
// say; it is kept for the server's log and no word of it reaches the caller. A
// generated method's frame answers a row it cannot locate with TargetNotFound, its gate
// (ExecuteGate) answers with it too, and so does a computed record's file route for a
// row its read does not show, so every way a handler finds no row reads the same. A
// handler that looks for a row itself answers with it as well. It answers the type
// httpio's own answers do, and is returned as one of those is.
func TargetNotFound(label string, key ...any) errors.Chain {
	return httpio.NewNotFoundMessageWithError(targetNotFoundCause(label, key), doesNotExist(label))
}

// targetNotFoundCause is what the server's log keeps of a row that is not there:
// the record's struct name and the row's key, in the words the answer itself said
// before it was put in a person's.
func targetNotFoundCause(label string, key []any) error {
	if len(key) == 0 {
		return errors.Newf("this %s does not exist", label)
	}
	values := make([]string, 0, len(key))
	for _, value := range key {
		values = append(values, fmt.Sprint(value))
	}

	return errors.Newf("%s %s does not exist", label, strings.Join(values, ", "))
}

// conditionRefused is the answer to a write whose grant holds for some rows and not for
// the one written: the sentence says the person may do it to others.
func (q *QuerySet[Resource]) conditionRefused(resources []accesstypes.Resource) error {
	cause := errors.Newf("scope (%s), user (%s): (%s) on %s is conditionally granted and the condition does not hold for this row",
		q.scope, q.userPermissions.User(), q.requiredPermission, resources)

	return httpio.NewForbiddenMessageWithError(cause, mayNotThisOne(q.requiredPermission, resources))
}

// valueRefused is the answer to a write whose grant would hold for the row written with
// another value: the sentence names the fields at fault, those whose values the grant's
// condition read, and says the value is what may not be set. The cause names the group's
// resources, as conditionRefused's does.
func (q *QuerySet[Resource]) valueRefused(resources, atFault []accesstypes.Resource) error {
	cause := errors.Newf("scope (%s), user (%s): (%s) on %s is conditionally granted and the condition does not hold for the values sent",
		q.scope, q.userPermissions.User(), q.requiredPermission, resources)

	return httpio.NewForbiddenMessageWithError(cause, mayNotToThisValue(q.requiredPermission, labelOf[Resource](), atFault))
}

// mayNot is the sentence for a permission refused on the resources denied, in the place
// where: "You may not change the budget of hangar passes here."
func mayNot(perm accesstypes.Permission, denied []accesstypes.Resource, where string) string {
	verb, things := permissionVerb(perm), thingsInWords(denied, "")
	if verb == "" || things == "" {
		return refusedPlainly
	}

	return "You may not " + verb + " " + things + " " + where + "."
}

// mayNotAskBy is the sentence for a list that may not be asked for by a field, which the
// caller may not see: "You may not sort or filter hangar passes by the budget here."
func mayNotAskBy(how string, field accesstypes.Resource, where string) string {
	name, tag, _ := strings.Cut(string(field), ".")
	thing, by := inWords(name), inWords(tag)
	if thing == "" || by == "" {
		return refusedPlainly
	}

	return "You may not " + how + " " + thing + " by the " + by + " " + where + "."
}

// mayNotThisOne is the sentence for a permission that holds for some rows and not for the
// one asked about: "You may change some hangar passes here, but not this one.", and for
// fields "You may change the budget of some hangar passes here, but not of this one."
func mayNotThisOne(perm accesstypes.Permission, resources []accesstypes.Resource) string {
	verb, things := permissionVerb(perm), thingsInWords(resources, "some ")
	if verb == "" || things == "" {
		return refusedPlainly
	}
	last := "but not this one."
	if strings.HasPrefix(things, "the ") {
		last = "but not of this one."
	}

	return "You may " + verb + " " + things + " " + refusedHere + ", " + last
}

// mayNotToThisValue is the sentence for a write whose grant refuses a value it sends, where
// the row would take another: "You may change the budget of this hangar pass, but not to
// this value." for a change, the record said by its struct's name in words, and "You may
// add hangar passes here, but not with this value for the budget." for a new row, the
// resource said by its name. Several fields read "the budget or the due date" and "these
// values". A permission that sends no value, a new row's resource with no words or no field
// with words names nothing.
func mayNotToThisValue(perm accesstypes.Permission, label string, atFault []accesstypes.Resource) string {
	var (
		name   string
		fields []string
	)
	for _, res := range atFault {
		resource, tag, _ := strings.Cut(string(res), ".")
		if name == "" {
			name = resource
		}
		if field := inWords(tag); field != "" {
			fields = append(fields, "the "+field)
		}
	}
	if len(fields) == 0 {
		return refusedPlainly
	}
	value := "this value"
	if len(fields) > 1 {
		value = "these values"
	}

	switch perm {
	case accesstypes.Update:
		return "You may " + permissionVerb(perm) + " " + eitherOf(fields) + " of this " + recordInWords(label) + ", but not to " + value + "."
	case accesstypes.Create:
		thing := inWords(name)
		if thing == "" {
			return refusedPlainly
		}

		return "You may " + permissionVerb(perm) + " " + thing + " " + refusedHere + ", but not with " + value + " for " + eitherOf(fields) + "."
	default:
		return refusedPlainly
	}
}

// mayNotDoThisTo is the sentence for a method refused on the row it names: "You may not
// do this to this mission." The record is said by its struct's name in words.
func mayNotDoThisTo(label string) string {
	thing := inWords(label)
	if thing == "" {
		return refusedPlainly
	}

	return "You may not do this to this " + thing + "."
}

// doesNotExist is the sentence for a row that is not there for the caller: "This mission
// does not exist." The record is said by its struct's name in words.
func doesNotExist(label string) string {
	return "This " + recordInWords(label) + " does not exist."
}

// hasNoFile is the sentence for a row that is there and carries no file under the
// segment asked for: "This mission document has no content." The record is said by its
// struct's name in words and the file by its route's segment in words.
func hasNoFile(label, segment string) string {
	return "This " + recordInWords(label) + " has no " + fileInWords(segment) + "."
}

// fileWasNotFound is the sentence for a row whose file is not where it was kept: "The
// content of this mission document was not found." It says nothing of where files are
// kept.
func fileWasNotFound(label, segment string) string {
	return "The " + fileInWords(segment) + " of this " + recordInWords(label) + " was not found."
}

// recordInWords says a record by its struct's name in words, "mission document", and a
// record with no name as "record".
func recordInWords(label string) string {
	if thing := inWords(label); thing != "" {
		return thing
	}

	return "record"
}

// fileInWords says a file by its route's segment in words, "flight plan", and a segment
// with no words as "file".
func fileInWords(segment string) string {
	if file := inWords(segment); file != "" {
		return file
	}

	return "file"
}

// flagHistoryWords is what a caller who may see none of the facts a flag's history tells
// is told.
const flagHistoryWords = "You may not see this flag's history here."

// actingAsRoleWords is what the person behind a session that acts as a role the
// application runs no sessions as is told when the session is refused an answer. What
// was asked for is not known where it is refused, so the sentence says no act.
const actingAsRoleWords = "You are acting as a role, and this session cannot do this."

// actingAsSomeoneCannot is the sentence for an act the mask of a session that acts as
// someone refuses: "You are acting as someone else, and this session cannot create a
// tenant." act is the handler's own words for what it does, the end of "this session
// cannot".
func actingAsSomeoneCannot(act string) string {
	return "You are acting as someone else, and this session cannot " + act + "."
}

// thingsInWords says the resources a refusal is about. A resource is its name in words,
// after quantity ("some hangar passes"); fields of a resource that is not itself among
// them are "the budget or the due date of" it. Resources that have no words say "".
func thingsInWords(resources []accesstypes.Resource, quantity string) string {
	var (
		names  []string
		fields = map[string][]string{}
		whole  = map[string]bool{}
	)
	for _, res := range resources {
		name, tag, _ := strings.Cut(string(res), ".")
		if _, seen := fields[name]; !seen {
			names = append(names, name)
			fields[name] = nil
		}
		if field := inWords(tag); field != "" {
			fields[name] = append(fields[name], "the "+field)
		} else {
			whole[name] = true
		}
	}

	things := make([]string, 0, len(names))
	for _, name := range names {
		thing := inWords(name)
		switch {
		case thing == "":
			continue
		case whole[name]:
			things = append(things, quantity+thing)
		default:
			things = append(things, eitherOf(fields[name])+" of "+quantity+thing)
		}
	}

	return eitherOf(things)
}

// eitherOf joins the things a person may not do something to: "a", "a or b", "a, b or c".
func eitherOf(things []string) string {
	if len(things) < 2 {
		return strings.Join(things, "")
	}

	return strings.Join(things[:len(things)-1], ", ") + " or " + things[len(things)-1]
}

// inWords spells a resource's name, or a field's, as lower-case words: "HangarPasses" is
// "hangar passes", "dueDate" is "due date". A resource's name is its plural already.
func inWords(name string) string {
	return words.InWords(name)
}
