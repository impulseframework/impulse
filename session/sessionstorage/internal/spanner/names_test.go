package spanner

import (
	"testing"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUsernameIndexFollowsUserTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		table string
		want  string
	}{
		{name: "the default table", table: "SessionUsers", want: "SessionUsersByNormalizedUsername"},
		{name: "a group's own table", table: "PartnerSessionUsers", want: "PartnerSessionUsersByNormalizedUsername"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SessionStorageDriver{}
			if err := s.SetUserTableName(tt.table); err != nil {
				t.Fatalf("SetUserTableName() error = %v", err)
			}
			if got := s.usernameIndex(); got != tt.want {
				t.Errorf("usernameIndex() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUsernameConflict holds a username conflict to both of the user table's unique
// indexes on the name: a name that repeats as written repeats normalized too, and the
// database names either index in its refusal.
func TestUsernameConflict(t *testing.T) {
	t.Parallel()

	refusal := func(code codes.Code, text string) error {
		return spanner.ToSpannerError(status.Error(code, text))
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "the normalized name's index", err: refusal(codes.AlreadyExists, "Unique index violation on index PartnerSessionUsersByNormalizedUsername at index key [ada]"), want: true},
		{name: "the exact name's index", err: refusal(codes.AlreadyExists, "Unique index violation on index PartnerSessionUsersByUsername at index key [ada]"), want: true},
		{name: "the address index is no username conflict", err: refusal(codes.AlreadyExists, "Unique index violation on index PartnerSessionUsersByNormalizedEmail at index key [a@example.com]"), want: false},
		{name: "another table's index of the same suffix", err: refusal(codes.AlreadyExists, "Unique index violation on index StaffSessionUsersByUsername at index key [ada]"), want: false},
		{name: "another code naming the index", err: refusal(codes.FailedPrecondition, "PartnerSessionUsersByUsername"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SessionStorageDriver{}
			if err := s.SetUserTableName("PartnerSessionUsers"); err != nil {
				t.Fatalf("SetUserTableName() error = %v", err)
			}
			if got := s.usernameConflict(tt.err); got != tt.want {
				t.Errorf("usernameConflict() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTableNameSettersValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  func(s *SessionStorageDriver, name string) error
		get  func(s *SessionStorageDriver) string
	}{
		{name: "SetSessionTableName", set: (*SessionStorageDriver).SetSessionTableName, get: func(s *SessionStorageDriver) string { return s.sessionTableName }},
		{name: "SetUserTableName", set: (*SessionStorageDriver).SetUserTableName, get: func(s *SessionStorageDriver) string { return s.userTableName }},
		{name: "SetIdentitiesTableName", set: (*SessionStorageDriver).SetIdentitiesTableName, get: func(s *SessionStorageDriver) string { return s.identitiesTableName }},
		{name: "SetPendingSignInsTableName", set: (*SessionStorageDriver).SetPendingSignInsTableName, get: func(s *SessionStorageDriver) string { return s.pendingTableName }},
		{name: "SetTokensTableName", set: (*SessionStorageDriver).SetTokensTableName, get: func(s *SessionStorageDriver) string { return s.tokensTableName }},
		{name: "SetDeviceCodesTableName", set: (*SessionStorageDriver).SetDeviceCodesTableName, get: func(s *SessionStorageDriver) string { return s.deviceCodesTableName }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewSessionStorageDriver(nil)
			before := tt.get(s)
			if err := tt.set(s, `Staff"; DROP TABLE`); err == nil {
				t.Errorf("%s() with a name that is not an identifier error = nil, want a refusal", tt.name)
			}
			if got := tt.get(s); got != before {
				t.Errorf("%s() changed the name to %q on a refusal, want %q kept", tt.name, got, before)
			}
			if err := tt.set(s, "Group_Table2"); err != nil {
				t.Errorf("%s() with an identifier error = %v", tt.name, err)
			}
		})
	}
}
