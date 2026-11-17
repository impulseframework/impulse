package resource

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/httpio"
)

// TestCheckWarning pins what a method's warning must carry before a frame answers it:
// the sentences a person reads and the words for the button. A warning that lacks either
// is the method's error, named for the method, and never an empty dialog.
func TestCheckWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		warning *Warning
		wantErr string
	}{
		{name: "a consequence and the button's words", warning: &Warning{Consequence: "The Kestrel is taken out of service for good.", Confirm: "Retire the Kestrel"}},
		{name: "no warning at all", wantErr: "RetireShip.Warning answered no warning"},
		{name: "no consequence", warning: &Warning{Consequence: " \n", Confirm: "Retire the Kestrel"}, wantErr: "RetireShip.Warning answered a warning with no consequence"},
		{name: "no words for the button", warning: &Warning{Consequence: "The Kestrel is taken out of service for good.", Confirm: "  "}, wantErr: "RetireShip.Warning answered a warning with no words for the confirm button"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckWarning("RetireShip", tt.warning)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CheckWarning() error = %v, want containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("CheckWarning() error = %v", err)
			}
		})
	}
}

// TestAsksWarning pins how a request asks for a method's warning in place of the call:
// the X-Warning header saying true, in any case, or no such header. Any other value is
// refused, so a request that meant to ask for the warning never runs the call instead.
func TestAsksWarning(t *testing.T) {
	t.Parallel()

	runHeaderTests(t, WarningHeader, AsksWarning)
}

// TestIsDryRun pins how a request asks for a dry run: the X-Dry-Run header saying
// true, in any case, or no such header. Any other value is refused, so a client that
// sends "1" to rehearse a call is told so and never writes for real.
func TestIsDryRun(t *testing.T) {
	t.Parallel()

	runHeaderTests(t, DryRunHeader, IsDryRun)
}

// runHeaderTests holds one of the two readers to the value its header takes.
func runHeaderTests(t *testing.T, header string, asks func(r *http.Request) (bool, error)) {
	t.Helper()

	tests := []struct {
		name string
		// values are the header's values as the request carries them, one for each time
		// the header is sent; none for a request without the header.
		values []string
		want   bool
		// wantRefused is the value the refusal quotes back.
		wantRefused string
	}{
		{name: "the header says true", values: []string{"true"}, want: true},
		{name: "in any case", values: []string{"TRUE"}, want: true},
		{name: "no header: the call itself"},
		{name: "the header says 1: refused, never the call itself", values: []string{"1"}, wantRefused: `"1"`},
		{name: "the header says false: refused", values: []string{"false"}, wantRefused: `"false"`},
		{name: "the header says yes: refused", values: []string{"yes"}, wantRefused: `"yes"`},
		{name: "an empty header: refused", values: []string{""}, wantRefused: `""`},
		{name: "the header sent twice: refused, whatever each says", values: []string{"true", "true"}, wantRefused: `"true, true"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/retire-ship", http.NoBody)
			for _, value := range tt.values {
				r.Header.Add(strings.ToLower(header), value)
			}
			got, err := asks(r)
			if tt.wantRefused != "" {
				want := "the " + header + " header takes the value true, or is left off the request: " + tt.wantRefused + " is not a value it takes, so the call was not run"
				if !httpio.HasBadRequest(err) || httpio.Message(err) != want {
					t.Fatalf("error = %v, want the BadRequest %q", err, want)
				}
				if got {
					t.Error("a refused header asked for something, want false")
				}

				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWarningAsked pins the sentinel a frame ends its transaction by once it has read a
// warning: it is told from every other error, wrapped or not.
func TestWarningAsked(t *testing.T) {
	t.Parallel()

	if !WarningAsked(ErrWarningAsked) || !WarningAsked(errors.Wrap(ErrWarningAsked, "spanner.Client.ReadWriteTransaction()")) {
		t.Error("WarningAsked() = false for the sentinel, want true bare and wrapped")
	}
	if WarningAsked(nil) || WarningAsked(errors.New("the row is gone")) {
		t.Error("WarningAsked() = true for no error or another error, want false")
	}
}
