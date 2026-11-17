package resource

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
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
// the X-Warning header saying true, in any case, and nothing else.
func TestAsksWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header map[string]string
		want   bool
	}{
		{name: "the header says true", header: map[string]string{WarningHeader: "true"}, want: true},
		{name: "in any case", header: map[string]string{"x-warning": "TRUE"}, want: true},
		{name: "no header: the call itself"},
		{name: "the header says false: the call itself", header: map[string]string{WarningHeader: "false"}},
		{name: "an empty header: the call itself", header: map[string]string{WarningHeader: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/api/retire-ship", http.NoBody)
			for name, value := range tt.header {
				r.Header.Set(name, value)
			}
			if got := AsksWarning(r); got != tt.want {
				t.Errorf("AsksWarning() = %v, want %v", got, tt.want)
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
