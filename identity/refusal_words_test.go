package identity

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/httpio"
)

// toldPlainly holds a refusal to what a person may be told: it is Forbidden, the person
// is sent the one sentence and nothing else, the sentence carries none of the words
// kept from them (a role's name), and the error's own text, which the server's log
// prints, still carries each.
func toldPlainly(t *testing.T, err error, sentence string, kept ...string) {
	t.Helper()

	if err == nil || !httpio.HasForbidden(err) {
		t.Fatalf("error = %v, want Forbidden saying %q", err, sentence)
	}
	messages := httpio.Messages(err)
	if len(messages) != 1 || messages[0] != sentence {
		t.Errorf("the person is told %q, want only %q", messages, sentence)
	}
	for _, word := range kept {
		if strings.Contains(strings.Join(messages, " "), word) {
			t.Errorf("the person is told %q, which carries %q", messages, word)
		}
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error's text, which the server's log prints, is %q: it does not carry %q", err.Error(), word)
		}
	}
}
