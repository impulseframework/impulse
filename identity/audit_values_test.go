package identity

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// fakeSecrets are the values the fakes hold or hand back that no row of the audit log
// holds. The session library keeps a person's password, its hash, a token, a link and a
// sign-in code to itself: what the package is handed of any of them is the hash of an
// invitation's token, in the row of an invitation the session library prepares.
var fakeSecrets = []struct {
	what  string
	value []byte
}{
	{what: "the invitation's token hash", value: []byte(preparedTokenHash)},
}

// secretForms are the texts a secret may be written as: as it is, in hex and in base64.
func secretForms(value []byte) []string {
	return []string{
		string(value), hex.EncodeToString(value),
		base64.StdEncoding.EncodeToString(value), base64.RawURLEncoding.EncodeToString(value),
	}
}

// heldSecret answers the secret of fakeSecrets a part of the record holds, in any of its
// forms, and false where none holds one. Every field of the record is searched as text,
// who acted among them, with the columns the change history keeps of it.
func heldSecret(record *auditlog.Record) (string, bool) {
	var texts []string
	fields := reflect.ValueOf(record).Elem()
	for i := range fields.NumField() {
		texts = append(texts, fmt.Sprint(fields.Field(i).Interface()))
	}
	for _, value := range record.Source.Columns() {
		texts = append(texts, fmt.Sprint(value))
	}
	for _, secret := range fakeSecrets {
		for _, form := range secretForms(secret.value) {
			for _, text := range texts {
				if strings.Contains(text, form) {
					return secret.what, true
				}
			}
		}
	}

	return "", false
}

// TestAuditRecordsHoldNoSecret pins the search every row the package writes passes: the
// fake store refuses a record that holds a secret of the fakes (fakeStore's
// InsertAuditRecord), so each rule that recorded one would fail its own test. The
// search finds a secret in every field of a record, in who acted, and in each of the
// forms a secret may be written as, and finds none in a record that holds none.
func TestAuditRecordsHoldNoSecret(t *testing.T) {
	t.Parallel()

	secret := fakeSecrets[0]
	fields := reflect.TypeFor[auditlog.Record]()
	for i := range fields.NumField() {
		field := fields.Field(i)
		record := disabledBob()
		value := reflect.ValueOf(record).Elem().Field(i)
		switch {
		case field.Type.Kind() == reflect.String:
			value.SetString("by " + string(secret.value))
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String:
			value.Set(reflect.ValueOf([]string{"Editor", string(secret.value)}).Convert(field.Type))
		default:
			continue
		}
		if _, held := heldSecret(record); !held {
			t.Errorf("a record whose %s holds %s was not found holding it", field.Name, secret.what)
		}
	}

	ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: recordSession, Username: string(secret.value)},
		UserID:      impulse.NullUUIDFromUUID(recordActor),
	})
	acted := disabledBob()
	acted.Source = eventsource.UserEvent(ctx)
	if _, held := heldSecret(acted); !held {
		t.Errorf("a record whose actor's username is %s was not found holding it", secret.what)
	}

	written := []string{hex.EncodeToString(secret.value), base64.StdEncoding.EncodeToString(secret.value), base64.RawURLEncoding.EncodeToString(secret.value)}
	for _, form := range written {
		record := disabledBob()
		record.Reason = "covering " + form
		if _, held := heldSecret(record); !held {
			t.Errorf("a record that holds %s written as %q was not found holding it", secret.what, form)
		}
	}

	if what, held := heldSecret(disabledBob()); held {
		t.Errorf("a record that holds no secret was found holding %s", what)
	}
	store := &fakeStore{}
	planted := disabledBob()
	planted.SubjectName = "bob " + string(secret.value)
	if err := store.InsertAuditRecord(context.Background(), nil, planted); err == nil {
		t.Errorf("the fake store wrote a record that holds %s, want it refused", secret.what)
	}
}
