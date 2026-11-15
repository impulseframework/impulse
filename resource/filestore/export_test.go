package filestore

import (
	"context"
	"time"
)

// StartupProbes is how many probes the readiness check sends under a permission that
// stays refused: one at the start and one after each pause of the bound.
const StartupProbes = int64(startupWait/startupRetry) + 1

// OpenBucketSkippingStartupWaits opens the bucket store as Open does for gs://<name>
// and runs its readiness check with the pauses between the startup probes skipped: the
// clock the check reads moves by each pause, so every probe of the bound runs against
// the server and the test waits no real time. The bound itself is held by
// TestBucketCheck.
func OpenBucketSkippingStartupWaits(ctx context.Context, name string) (*Bucket, error) {
	b, err := openBucket(ctx, name)
	if err != nil {
		return nil, err
	}
	clock := time.Now()
	b.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)

		return ctx.Err()
	}
	b.now = func() time.Time {
		return clock
	}
	if err := b.Check(ctx); err != nil {
		_ = b.Close()

		return nil, err
	}

	return b, nil
}
