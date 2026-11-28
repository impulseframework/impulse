package resource

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestTenantDirectoryRules_TakeDirectoryWarning pins the statement a person reads before a
// tenant takes their organization's directory, word for word: it opens with the
// authorization, says in a note what the act does, in the organization's own name where
// a registration or the directory gives one and by the words of a label where none
// does, and puts what the button does on the button, for a tenant that stands and for
// one being created in the same act. Each passes CheckWarning.
func TestTenantDirectoryRules_TakeDirectoryWarning(t *testing.T) {
	t.Parallel()

	rules := NewTenantDirectoryRules("Stations", "Id", &TenantDeclarationColumns{}, "station")
	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "0c5f6f8e-0000-4000-8000-000000000001"}

	tests := []struct {
		name      string
		directory signin.Directory
		creating  bool
		want      *Warning
	}{
		{
			name:      "a named Workspace, for a station that stands",
			directory: workspace,
			want: &Warning{
				Consequence: "You are stating that you are authorized to act on behalf of lakeside.edu.",
				Also: []WarningNote{{
					Says: "From now on, people at lakeside.edu who sign in through it and have no account here are sent to this station. They get in by your invitation, or with a starting role if you open the station to them. No other station can use lakeside.edu.",
				}},
				Confirm: "Make lakeside.edu this station's directory",
			},
		},
		{
			name:      "a named Workspace, for the station being created",
			directory: workspace,
			creating:  true,
			want: &Warning{
				Consequence: "You are stating that you are authorized to act on behalf of lakeside.edu.",
				Also: []WarningNote{{
					Says: "From now on, people at lakeside.edu who sign in through it and have no account here are sent to the station you are creating. They get in by your invitation, or with a starting role if you open the station to them. No other station can use lakeside.edu.",
				}},
				Confirm: "Create the station with lakeside.edu as its directory",
			},
		},
		{
			name:      "an Entra tenant nobody names, for a station that stands",
			directory: entra,
			want: &Warning{
				Consequence: "You are stating that you are authorized to act on behalf of your organization.",
				Also: []WarningNote{{
					Says: "From now on, people who sign in through your organization's Microsoft directory and have no account here are sent to this station. They get in by your invitation, or with a starting role if you open the station to them. No other station can use that directory.",
				}},
				Confirm: "Make it this station's directory",
			},
		},
		{
			name:      "an Entra tenant nobody names, for the station being created",
			directory: entra,
			creating:  true,
			want: &Warning{
				Consequence: "You are stating that you are authorized to act on behalf of your organization.",
				Also: []WarningNote{{
					Says: "From now on, people who sign in through your organization's Microsoft directory and have no account here are sent to the station you are creating. They get in by your invitation, or with a starting role if you open the station to them. No other station can use that directory.",
				}},
				Confirm: "Create the station with your organization's directory",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rules.TakeDirectoryWarning(tt.directory, tt.creating)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("TakeDirectoryWarning() mismatch (-want +got):\n%s", diff)
			}
			if err := CheckWarning("TakeStationDirectory", got); err != nil {
				t.Errorf("CheckWarning() error = %v, want a statement a dialog can show", err)
			}
		})
	}

	// A registration's name for the Entra tenant is the organization's own name.
	named := rules.NamedBy(func(signin.Directory) (name, label string) { return "Lakeside", "Microsoft" })
	want := &Warning{
		Consequence: "You are stating that you are authorized to act on behalf of Lakeside.",
		Also: []WarningNote{{
			Says: "From now on, people at Lakeside who sign in through it and have no account here are sent to this station. They get in by your invitation, or with a starting role if you open the station to them. No other station can use Lakeside.",
		}},
		Confirm: "Make Lakeside this station's directory",
	}
	if diff := cmp.Diff(want, named.TakeDirectoryWarning(entra, false)); diff != "" {
		t.Errorf("TakeDirectoryWarning() of named rules mismatch (-want +got):\n%s", diff)
	}
	if TakeDirectoryPhrase != "I am authorized" {
		t.Errorf("TakeDirectoryPhrase = %q, want the words a person types under the statement", TakeDirectoryPhrase)
	}
}

// TestTenantDirectoryRules_OpenTakesPeopleNote pins the note an application that holds a
// person to one tenant adds before a tenant is opened to its directory: the directory's
// people who belong to another tenant are moved at their next sign-in.
func TestTenantDirectoryRules_OpenTakesPeopleNote(t *testing.T) {
	t.Parallel()

	rules := NewTenantDirectoryRules("Stations", "Id", &TenantDeclarationColumns{}, "station")
	entra := &TenantDirectory{Key: "beta-0", Directory: signin.Directory{Method: sessioninfo.MethodAzure, Connection: "0c5f6f8e-0000-4000-8000-000000000001"}}
	want := WarningNote{
		Says: "A person belongs to one station here. People who sign in through your organization's Microsoft directory and belong to another station are moved to this one the next time they sign in, and lose everything they hold in the other.",
	}
	if diff := cmp.Diff(want, rules.OpenTakesPeopleNote(entra)); diff != "" {
		t.Errorf("OpenTakesPeopleNote() mismatch (-want +got):\n%s", diff)
	}
}
