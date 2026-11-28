package spanner

import (
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSessionStorageDriver_SignInConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want dbtype.SignInConflict
	}{
		{
			name: "the normalized username index refusing a provisioned user's name",
			err:  status.Error(codes.AlreadyExists, "Unique index violation on index SessionUsersByNormalizedUsername"),
			want: dbtype.UsernameConflict,
		},
		{
			name: "the address index refusing an address another user holds",
			err:  status.Error(codes.AlreadyExists, "Unique index violation on index SessionUsersByNormalizedEmail"),
			want: dbtype.EmailConflict,
		},
		{
			name: "a wrapped address conflict",
			err:  errors.Wrap(status.Error(codes.AlreadyExists, "Unique index violation on index SessionUsersByNormalizedEmail"), "spannertxn.Serializable()"),
			want: dbtype.EmailConflict,
		},
		{
			name: "the identities table refusing a link",
			err:  status.Error(codes.AlreadyExists, "Row [method, connection, subject] in table SessionIdentities already exists"),
			want: dbtype.IdentityConflict,
		},
		{
			name: "an error that is not an AlreadyExists",
			err:  status.Error(codes.Aborted, "transaction aborted"),
			want: dbtype.NoSignInConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SessionStorageDriver{}
			if err := s.SetUserTableName("SessionUsers"); err != nil {
				t.Fatalf("SetUserTableName() error = %v", err)
			}
			if got := s.SignInConflict(tt.err); got != tt.want {
				t.Errorf("SignInConflict() = %v, want %v", got, tt.want)
			}
		})
	}
}
