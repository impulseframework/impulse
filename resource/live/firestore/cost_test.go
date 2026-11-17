package firestore_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/resource/live"
	livefirestore "github.com/impulseframework/impulse/resource/live/firestore"
	"google.golang.org/grpc"
)

// storeCost is what one step cost the store: the queries it ran, the documents those
// queries returned (what Firestore bills as reads, with a floor of one per query), and
// the documents it wrote or deleted.
type storeCost struct {
	Queries int
	Read    int
	Written int
}

// costCounter counts the store's operations on one connection, through the gRPC
// interceptors the service's test dial option installs.
type costCounter struct {
	mu   sync.Mutex
	cost storeCost
}

// take returns what the store was asked since the last take.
func (c *costCounter) take() storeCost {
	c.mu.Lock()
	defer c.mu.Unlock()
	cost := c.cost
	c.cost = storeCost{}

	return cost
}

func (c *costCounter) add(cost storeCost) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cost.Queries += cost.Queries
	c.cost.Read += cost.Read
	c.cost.Written += cost.Written
}

// unary counts the documents a write request carries.
func (c *costCounter) unary(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	switch request := req.(type) {
	case *firestorepb.BatchWriteRequest:
		c.add(storeCost{Written: len(request.GetWrites())})
	case *firestorepb.CommitRequest:
		c.add(storeCost{Written: len(request.GetWrites())})
	}

	return invoker(ctx, method, req, reply, conn, opts...)
}

// stream counts a query and the documents it returns.
func (c *costCounter) stream(ctx context.Context, desc *grpc.StreamDesc, conn *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	stream, err := streamer(ctx, desc, conn, method, opts...)
	if err != nil || !strings.HasSuffix(method, "/RunQuery") {
		return stream, err
	}
	c.add(storeCost{Queries: 1})

	return &countedQuery{ClientStream: stream, counter: c}, nil
}

// countedQuery counts the documents one query's stream delivers.
type countedQuery struct {
	grpc.ClientStream
	counter *costCounter
}

func (q *countedQuery) RecvMsg(m any) error {
	err := q.ClientStream.RecvMsg(m)
	if response, ok := m.(*firestorepb.RunQueryResponse); ok && err == nil && response.GetDocument() != nil {
		q.counter.add(storeCost{Read: 1})
	}

	return err //nolint:wrapcheck // the stream's own error, io.EOF among them, is what its reader compares
}

// TestService_cost pins what live lists and rows cost the store: the documents the
// record holds, and the queries, the documents read and the documents written by a
// subscribe, a renewal, a publish and an unsubscribe. The numbers are the cost of one
// tenant's list, a global list and a row, which is every subscription an application
// without a list across tenants makes: a change to how the record keeps a subscription
// must leave every one of them as it is.
func TestService_cost(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	counter := &costCounter{}
	svc := newServiceIn(t, testDatabase,
		livefirestore.WithClock(fixedClock(now)),
		livefirestore.WithDialOptions(grpc.WithChainUnaryInterceptor(counter.unary), grpc.WithChainStreamInterceptor(counter.stream)),
	)
	hangars := resourceFor(t)
	clients := hangars + "_global"
	marshal, pilot, bosun, engineer := principalFor(t, "marshal"), principalFor(t, "pilot"), principalFor(t, "bosun"), principalFor(t, "engineer")
	expiry := now.Add(live.SubscriptionTTL)

	// stored is how many documents the record holds for the four people.
	stored := func() int {
		t.Helper()

		total := 0
		for _, principal := range []string{marshal, pilot, bosun, engineer} {
			n, err := livefirestore.StoredSubscriptions(t.Context(), svc, principal)
			if err != nil {
				t.Fatalf("StoredSubscriptions() error = %v", err)
			}
			total += n
		}
		counter.take()

		return total
	}
	rows := func(res accesstypes.Resource, n int) live.Writes {
		changes := make([]resource.RowChange, 0, n)
		for i := range n {
			changes = append(changes, resource.RowChange{Key: "h" + strings.Repeat("x", i)})
		}

		return live.Writes{ID: "write-1", Rows: map[accesstypes.Resource][]resource.RowChange{res: changes}}
	}

	publish := func(ctx context.Context, domain accesstypes.Domain, writes live.Writes) error {
		if _, err := svc.Publish(ctx, domain, writes); err != nil {
			return fmt.Errorf("Publish(): %w", err)
		}

		return nil
	}
	renew := func(ctx context.Context, subs ...live.Subscription) error {
		refused, err := svc.Renew(ctx, subs)
		if err != nil {
			return fmt.Errorf("Renew(): %w", err)
		}
		if len(refused) != 0 {
			return fmt.Errorf("Renew() refused %d subscriptions, the first: %w", len(refused), refused[0].Err)
		}

		return nil
	}

	anvilList := live.Subscription{Principal: marshal, Tab: "t1", Resource: hangars, Domain: "anvil", Expiry: expiry}
	steps := []struct {
		name       string
		run        func(ctx context.Context) error
		want       storeCost
		wantStored int
	}{
		{
			name: "a tenant's list is subscribed: one document written, nothing read",
			run: func(ctx context.Context) error {
				return svc.Register(ctx, []live.Subscription{anvilList})
			},
			want:       storeCost{Written: 1},
			wantStored: 1,
		},
		{
			name: "it is renewed: the same document written again",
			run: func(ctx context.Context) error {
				renewed := anvilList
				renewed.Expiry = expiry.Add(live.RenewInterval)

				return renew(ctx, renewed)
			},
			want:       storeCost{Written: 1},
			wantStored: 1,
		},
		{
			name: "three more people subscribe: the same list, another tenant's, and a row",
			run: func(ctx context.Context) error {
				return svc.Register(ctx, []live.Subscription{
					{Principal: pilot, Tab: "t2", Resource: hangars, Domain: "anvil", Expiry: expiry},
					{Principal: bosun, Tab: "t3", Resource: hangars, Domain: "bastion", Expiry: expiry},
					{Principal: engineer, Tab: "t4", Resource: hangars, Key: "h", Domain: "anvil", Expiry: expiry},
				})
			},
			want:       storeCost{Written: 3},
			wantStored: 4,
		},
		{
			name: "a row is written in the tenant: the list's two subscribers and the row's one are read, each is told",
			run: func(ctx context.Context) error {
				return publish(ctx, "anvil", rows(hangars, 1))
			},
			want:       storeCost{Queries: 2, Read: 3, Written: 3},
			wantStored: 4,
		},
		{
			name: "a row nobody holds is written in the other tenant: its list's one subscriber is read and told",
			run: func(ctx context.Context) error {
				return publish(ctx, "bastion", live.Writes{ID: "write-2", Rows: map[accesstypes.Resource][]resource.RowChange{hangars: {{Key: "other"}}}})
			},
			want:       storeCost{Queries: 2, Read: 1, Written: 1},
			wantStored: 4,
		},
		{
			name: "a row is written in a tenant nobody lists: two queries, nothing read, nothing written",
			run: func(ctx context.Context) error {
				return publish(ctx, "cinder", live.Writes{ID: "write-3", Rows: map[accesstypes.Resource][]resource.RowChange{hangars: {{Key: "other"}}}})
			},
			want:       storeCost{Queries: 2},
			wantStored: 4,
		},
		{
			name: "over the threshold in the tenant: one query reads the resource's four subscribers, three are told",
			run: func(ctx context.Context) error {
				return publish(ctx, "anvil", rows(hangars, live.BulkThreshold+1))
			},
			want:       storeCost{Queries: 1, Read: 4, Written: 3},
			wantStored: 4,
		},
		{
			name: "a global list and one of its rows are subscribed",
			run: func(ctx context.Context) error {
				return svc.Register(ctx, []live.Subscription{
					{Principal: marshal, Tab: "t1", Resource: clients, Expiry: expiry},
					{Principal: pilot, Tab: "t2", Resource: clients, Key: "h", Expiry: expiry},
				})
			},
			want:       storeCost{Written: 2},
			wantStored: 6,
		},
		{
			name: "the global row is written: the list's query also reads the row's document, as it always has",
			run: func(ctx context.Context) error {
				return publish(ctx, "", rows(clients, 1))
			},
			want:       storeCost{Queries: 2, Read: 3, Written: 2},
			wantStored: 6,
		},
		{
			name: "a tab leaves: its two documents are read and deleted",
			run: func(ctx context.Context) error {
				return svc.Unsubscribe(ctx, marshal, "t1")
			},
			want:       storeCost{Queries: 1, Read: 2, Written: 2},
			wantStored: 4,
		},
	}

	if got := stored(); got != 0 {
		t.Fatalf("the record holds %d documents before the first step, want none", got)
	}
	for _, step := range steps {
		if err := step.run(t.Context()); err != nil {
			t.Fatalf("%s: error = %v", step.name, err)
		}
		got := counter.take()
		t.Logf("%-110s queries %d, documents read %d, documents written %d", step.name+":", got.Queries, got.Read, got.Written)
		if diff := cmp.Diff(step.want, got); diff != "" {
			t.Errorf("%s: the store's cost mismatch (-want +got):\n%s", step.name, diff)
		}
		if got := stored(); got != step.wantStored {
			t.Errorf("%s: the record holds %d documents, want %d", step.name, got, step.wantStored)
		}
	}
}
