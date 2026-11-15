package db

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// firestoreTestVersion is the Cloud SDK version the tests start the emulator image of.
const firestoreTestVersion = "562.0.0"

// ownerCredentials is the emulator's owner credential, which the Firestore client itself
// sends when FIRESTORE_EMULATOR_HOST is set and which the rules never apply to.
type ownerCredentials struct{}

func (ownerCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer owner"}, nil
}

func (ownerCredentials) RequireTransportSecurity() bool {
	return false
}

func TestNewFirestoreContainer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	type args struct {
		ctx  context.Context
		opts []FirestoreOption
	}
	tests := []struct {
		name string
		args args
		// wantAnonymous is the status the emulator answers a read made without the owner
		// credential with: the rules decide it.
		wantAnonymous int
		wantErr       bool
	}{
		{
			name:          "Emulator without rules",
			args:          args{ctx: context.Background()},
			wantAnonymous: http.StatusNotFound,
		},
		{
			name:          "Emulator with the rules file",
			args:          args{ctx: context.Background(), opts: []FirestoreOption{WithFirestoreRules("testdata/firestore/firestore.rules")}},
			wantAnonymous: http.StatusForbidden,
		},
		{
			name:    "Rules file that does not exist",
			args:    args{ctx: context.Background(), opts: []FirestoreOption{WithFirestoreRules("testdata/firestore/missing.rules")}},
			wantErr: true,
		},
		{
			name:    "Container error from canceled context",
			args:    args{ctx: ctx},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			container, err := NewFirestoreContainer(tt.args.ctx, firestoreTestVersion, tt.args.opts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewFirestoreContainer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			t.Cleanup(func() {
				if err := container.Terminate(context.Background()); err != nil {
					t.Errorf("FirestoreContainer.Terminate() error = %v", err)
				}
				if err := container.Close(); err != nil {
					t.Errorf("FirestoreContainer.Close() error = %v", err)
				}
			})

			// The client talks to the emulator with the owner credential, which the rules
			// never apply to, as one opened with FIRESTORE_EMULATOR_HOST set does. The
			// connection carries it in place of the variable, which is the process's and
			// would hold the cases to one at a time.
			conn, err := grpc.NewClient(container.Host(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithPerRPCCredentials(ownerCredentials{}),
			)
			if err != nil {
				t.Fatalf("grpc.NewClient() error = %v", err)
			}
			client, err := firestore.NewClient(t.Context(), "unit-testing", option.WithGRPCConn(conn))
			if err != nil {
				t.Fatalf("firestore.NewClient() error = %v", err)
			}
			t.Cleanup(func() {
				if err := client.Close(); err != nil {
					t.Errorf("firestore.Client.Close() error = %v", err)
				}
			})

			doc := client.Collection("tests").Doc("written")
			if _, err := doc.Set(t.Context(), map[string]any{"name": tt.name}); err != nil {
				t.Fatalf("firestore.DocumentRef.Set() error = %v", err)
			}
			snapshot, err := doc.Get(t.Context())
			if err != nil {
				t.Fatalf("firestore.DocumentRef.Get() error = %v", err)
			}
			if got, err := snapshot.DataAt("name"); err != nil || got != tt.name {
				t.Errorf("firestore.DocumentSnapshot.DataAt() = %v, %v, want %q", got, err, tt.name)
			}

			if got := anonymousRead(t, container.Host()); got != tt.wantAnonymous {
				t.Errorf("a read without the owner credential answered %d, want %d", got, tt.wantAnonymous)
			}
		})
	}
}

// anonymousRead reads a document that was never written through the emulator's REST API
// without a credential, and returns the status: not found when the emulator has no rules,
// forbidden when its rules refuse the read.
func anonymousRead(t *testing.T, host string) int {
	t.Helper()

	url := fmt.Sprintf("http://%s/v1/projects/unit-testing/databases/(default)/documents/tests/never-written", host)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Client.Do() error = %v", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}
