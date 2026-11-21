package dbtype

import (
	"testing"
)

// TestValidateInsertUser_theUsernameIsTheAddress holds a new user of a store whose
// usernames are the addresses to one address: the username, in whatever case its ASCII
// letters are written. An address written with the Kelvin sign (U+212A), which Unicode
// folds to k, is another address than the ASCII username and is refused.
func TestValidateInsertUser_theUsernameIsTheAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		email    string
		wantErr  bool
	}{
		{name: "the address is the username", username: "kim@kirkwood.edu", email: "kim@kirkwood.edu"},
		{name: "the address is the username in another ASCII case", username: "kim@kirkwood.edu", email: " Kim@Kirkwood.EDU "},
		{name: "another address", username: "kim@kirkwood.edu", email: "lee@kirkwood.edu", wantErr: true},
		{name: "a name written with the Kelvin sign is another address", username: "kim@kirkwood.edu", email: "Kim@kirkwood.edu", wantErr: true},
		{name: "a domain written with the Kelvin sign is another address", username: "kim@kirkwood.edu", email: "kim@Kirkwood.edu", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateInsertUser(&InsertSessionUser{Username: tt.username, Email: &tt.email}, true)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateInsertUser() error = %v, want an error %v", err, tt.wantErr)
			}
		})
	}
}
