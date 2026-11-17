package dbtype

import "testing"

// TestSignInMethod_OpenFor pins the judgment every sign-in and every sign-up reads a
// method's row with: the row opens its method for the audience it was opened for and
// no other. No row is invite only, a row made invite only is, and so is an open row
// that says nothing of whom it was opened for, or names another audience than the
// method's registration has now, wider or narrower.
func TestSignInMethod_OpenFor(t *testing.T) {
	t.Parallel()

	const (
		lakeside = `{"organizations":["lakeside.edu"],"accounts":""}`
		both     = `{"organizations":["acme.example","lakeside.edu"],"accounts":""}`
	)
	opened := func(audience string) *string {
		return &audience
	}
	tests := []struct {
		name string
		row  *SignInMethod
		now  string
		want bool
	}{
		{name: "no row is invite only", row: nil, now: lakeside, want: false},
		{name: "a row opened for the audience the method has", row: &SignInMethod{Name: "google", OpenSignUp: true, OpenedFor: opened(lakeside)}, now: lakeside, want: true},
		{name: "a row made invite only, whatever audience it keeps", row: &SignInMethod{Name: "google", OpenSignUp: false, OpenedFor: opened(lakeside)}, now: lakeside, want: false},
		{name: "an open row that names no audience", row: &SignInMethod{Name: "google", OpenSignUp: true}, now: lakeside, want: false},
		{name: "a row opened for a narrower audience than the method has now", row: &SignInMethod{Name: "google", OpenSignUp: true, OpenedFor: opened(lakeside)}, now: both, want: false},
		{name: "a row opened for a wider audience than the method has now", row: &SignInMethod{Name: "google", OpenSignUp: true, OpenedFor: opened(both)}, now: lakeside, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.row.OpenFor(tt.now); got != tt.want {
				t.Errorf("SignInMethod.OpenFor(%s) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}
