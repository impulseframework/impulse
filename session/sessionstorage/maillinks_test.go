package sessionstorage

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/auditlog"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	gomock "go.uber.org/mock/gomock"
)

// TestAuth_InsertMailLink_theLinksID pins the id the store gives a mailed link: one the
// caller made (an invitation, whose record names it) is kept, and a link with none is
// given a new one. The record of the link's act is handed to the driver as it was handed.
func TestAuth_InsertMailLink_theLinksID(t *testing.T) {
	t.Parallel()

	made := impulse.Must(impulse.NewUUID())
	tests := []struct {
		name string
		id   impulse.UUID
		// kept says the driver is handed the id the caller made.
		kept bool
	}{
		{name: "an id the caller made is kept", id: made, kept: true},
		{name: "a link with no id is given one", id: impulse.NilUUID, kept: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)

			record := &auditlog.Record{
				Act: auditlog.ActInvited, Source: eventsource.ProcessEvent(auditlog.ProcessMigrate), SubjectKind: auditlog.SubjectInvitation,
				SubjectID: made.String(), SubjectName: "pat@lakeside.edu", RoleNames: []string{"Reader"}, RoleScope: auditlog.RoleScopeGlobal,
			}
			var stored impulse.UUID
			db := NewMockdb(ctrl)
			db.EXPECT().InsertMailLink(gomock.Any(), gomock.Any(), record).DoAndReturn(func(_ context.Context, l *dbtype.MailLink, _ *auditlog.Record) error {
				stored = l.ID

				return nil
			})
			a := &Auth[struct{}]{userStorage: userStorage{sessionStorage: sessionStorage{db: db}}}

			link := &MailLink{ID: tt.id, TokenHash: []byte("hash"), Kind: MailLinkInvitation, Email: "pat@lakeside.edu"}
			if err := a.InsertMailLink(t.Context(), link, record); err != nil {
				t.Fatalf("InsertMailLink() error = %v", err)
			}
			switch {
			case stored.IsNil() || link.ID != stored:
				t.Errorf("the driver stored the id %s and the link holds %s, want one id set on both", stored, link.ID)
			case tt.kept && stored != made:
				t.Errorf("the driver stored the id %s, want the one the caller made, %s", stored, made)
			}
		})
	}
}
