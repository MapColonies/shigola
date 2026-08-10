package s3_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"

	"github.com/go-spatial/tegola/cache"
	"github.com/go-spatial/tegola/cache/s3"
)

// TestContextIsHonoured pins that every cache/s3 operation reacts to the
// context it is handed.
//
// Set is the one that motivated this. It called PutObject, which attaches no
// context, so a caller's deadline had nothing to act through — and cache/s3
// builds aws.Config with no HTTPClient, so the SDK falls back to
// http.DefaultClient with Timeout: 0. An s3 write was therefore unbounded at
// the Go level, which is what permanently exhausts the detached write pool.
//
// No AWS credentials and no RUN_S3_TESTS gate: the endpoint is a local server
// that never answers, which is a better model of a wedged write than a real
// bucket is anyway.
func TestContextIsHonoured(t *testing.T) {
	// A server that accepts the request and then never responds.
	block := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))

	// Order matters, and deferred calls run last-in-first-out: release the
	// handlers *before* closing the server. httptest.Server.Close waits for
	// outstanding connections, so closing first deadlocks against a handler
	// that is still parked on the channel.
	defer srv.Close()
	defer close(block)

	sess, err := session.NewSession(&awssdk.Config{
		Region:           awssdk.String("us-east-1"),
		Endpoint:         awssdk.String(srv.URL),
		S3ForcePathStyle: awssdk.Bool(true),
		Credentials:      credentials.NewStaticCredentials("id", "secret", ""),
		// One attempt. The SDK retries by default, and a retry loop would make
		// this measure the retry policy rather than the context.
		MaxRetries: awssdk.Int(0),
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	c := &s3.Cache{
		Bucket:      "tiles",
		MaxZoom:     20,
		Client:      awss3.New(sess),
		ContentType: "application/vnd.mapbox-vector-tile",
	}

	key := &cache.Key{MapName: "osm", Z: 1, X: 1, Y: 1}

	type tcase struct {
		op func(context.Context) error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()

			start := time.Now()
			err := tc.op(ctx)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			// The SDK wraps it, so match on the message rather than the type —
			// what matters is that the deadline is what ended the call.
			if !errors.Is(err, context.DeadlineExceeded) &&
				!containsAny(err.Error(), "context deadline exceeded", "RequestCanceled") {
				t.Fatalf("got %v, expected the context deadline to end the call", err)
			}
			if elapsed > 5*time.Second {
				t.Errorf("returned after %v — the context was not attached to the request", elapsed)
			}
		}
	}

	tests := map[string]tcase{
		// The fix. Without PutObjectWithContext this hangs until the test's
		// own budget expires.
		"set": {op: func(ctx context.Context) error { return c.Set(ctx, key, testData) }},
		"get": {op: func(ctx context.Context) error { _, _, err := c.Get(ctx, key); return err }},
		"purge": {op: func(ctx context.Context) error {
			// Purge is ctx-aware today, but timeout_ms is Get-only so nothing
			// applies a deadline to it in production. Pinned so that stays a
			// choice rather than an accident.
			return c.Purge(ctx, key)
		}},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
