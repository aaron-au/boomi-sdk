package pace

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// keySeq makes every testKey call unique. The registries are process-wide
// and never reset, so state-sensitive tests need a fresh key per run —
// including repeated runs of the same test under -count.
var keySeq atomic.Int64

// testKey returns a Key no other test (or run) has used.
func testKey(name string) Key {
	return Key{
		Host:      fmt.Sprintf("%s-%d.example.test", name, keySeq.Add(1)),
		AccountID: "acct-" + name,
	}
}

// caseVariedKeys returns two keys equal but for Host casing, unique to
// this call.
func caseVariedKeys(name string) (lowerKey, upperKey Key) {
	lower := testKey(name)
	upper := lower
	upper.Host = strings.ToUpper(upper.Host)

	return lower, upper
}

// fakeClock is a manual clock: now returns the current fake time, and
// sleep advances it by the requested duration instead of waiting, so
// paced tests run with zero real sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.t
}

func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if d > 0 {
		f.mu.Lock()
		f.t = f.t.Add(d)
		f.mu.Unlock()
	}

	return nil
}

// installClock swaps the package clock seams for fc until the test ends.
// Tests using it must not run in parallel and must join their goroutines
// before returning.
func installClock(t *testing.T, fc *fakeClock) {
	t.Helper()

	savedNow, savedSleep := now, sleepCtx
	now = fc.now
	sleepCtx = fc.sleep

	t.Cleanup(func() { now = savedNow; sleepCtx = savedSleep })
}

// installNow swaps only the now seam for a fixed function.
func installNow(t *testing.T, f func() time.Time) {
	t.Helper()

	saved := now
	now = f

	t.Cleanup(func() { now = saved })
}

// installSleep swaps only the sleepCtx seam.
func installSleep(t *testing.T, f func(context.Context, time.Duration) error) {
	t.Helper()

	saved := sleepCtx
	sleepCtx = f

	t.Cleanup(func() { sleepCtx = saved })
}

// installRand swaps the jitter seam.
func installRand(t *testing.T, f func() float64) {
	t.Helper()

	saved := randFloat
	randFloat = f

	t.Cleanup(func() { randFloat = saved })
}

// --- registry -------------------------------------------------------------

func TestForAccountSameKeySameLimiter(t *testing.T) {
	k := testKey("registry-identity")

	first, second := ForAccount(k), ForAccount(k)
	if first != second {
		t.Fatal("ForAccount returned different limiters for equal keys")
	}
}

func TestForAccountHostCaseInsensitive(t *testing.T) {
	lower, upper := caseVariedKeys("registry-case")
	a := ForAccount(upper)

	b := ForAccount(lower)
	if a != b {
		t.Fatal("case-varied Host produced different limiters")
	}
}

func TestForAccountDistinctKeysDistinctLimiters(t *testing.T) {
	a := ForAccount(testKey("registry-a"))

	b := ForAccount(testKey("registry-b"))
	if a == b {
		t.Fatal("distinct keys shared a limiter")
	}

	c := ForAccount(Key{Host: "sub.example.test", AccountID: "acct", OverrideAccount: "one"})

	d := ForAccount(Key{Host: "sub.example.test", AccountID: "acct", OverrideAccount: "two"})
	if c == d {
		t.Fatal("distinct OverrideAccount shared a limiter")
	}
}

func TestForAccountConcurrent(t *testing.T) {
	k := testKey("registry-concurrent")

	const n = 64

	got := make([]*Limiter, n)

	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			got[i] = ForAccount(k)
		}(i)
	}

	wg.Wait()

	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatal("concurrent ForAccount returned different limiters")
		}
	}
}

// --- bucket ---------------------------------------------------------------

func TestBucketSpacing(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	l := ForAccount(testKey("bucket-spacing"))
	ctx := context.Background()

	var grants []time.Time

	for i := 0; i < 5; i++ {
		rel, _, err := l.Acquire(ctx, Read)
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}

		grants = append(grants, fc.now())

		rel()
	}

	for i := 1; i < len(grants); i++ {
		if d := grants[i].Sub(grants[i-1]); d < 125*time.Millisecond {
			t.Fatalf("grants %d and %d only %v apart, want >= 125ms", i-1, i, d)
		}
	}
	// Burst is 1: the first grant is immediate.
	if !grants[0].Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first grant waited: %v", grants[0])
	}
}

func TestBucketSpacingConcurrent(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	l := ForAccount(testKey("bucket-concurrent"))
	ctx := context.Background()
	start := fc.now()

	const n = 8

	var wg sync.WaitGroup

	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			rel, _, err := l.Acquire(ctx, Read)
			if err != nil {
				errs[i] = err
				return
			}

			rel()
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
	}
	// n grants at >= 125ms spacing need at least (n-1)*125ms of clock.
	if elapsed, wantMin := fc.now().Sub(start), time.Duration(n-1)*125*time.Millisecond; elapsed < wantMin {
		t.Fatalf("%d grants in %v of fake time, want >= %v", n, elapsed, wantMin)
	}
}

func TestSharedBudgetAcrossForAccountCalls(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	lower, upper := caseVariedKeys("shared-budget")
	l1 := ForAccount(upper)

	l2 := ForAccount(lower)
	if l1 != l2 {
		t.Fatal("expected one limiter for case-varied host")
	}

	ctx := context.Background()

	rel1, waited1, err := l1.Acquire(ctx, Read)
	if err != nil {
		t.Fatal(err)
	}

	rel1()

	if waited1 != 0 {
		t.Fatalf("first acquire waited %v, want 0", waited1)
	}

	rel2, waited2, err := l2.Acquire(ctx, Read)
	if err != nil {
		t.Fatal(err)
	}

	rel2()

	if waited2 < 125*time.Millisecond {
		t.Fatalf("second client's acquire waited %v, want >= 125ms: budget not shared", waited2)
	}
}

// --- classes --------------------------------------------------------------

func TestWriteExclusivity(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	l := ForAccount(testKey("write-exclusive"))
	ctx := context.Background()

	var (
		inWrite    atomic.Int32
		overlapped atomic.Bool
		wg         sync.WaitGroup
	)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := 0; i < 2; i++ {
				rel, _, err := l.Acquire(ctx, Write)
				if err != nil {
					t.Errorf("Acquire(Write): %v", err)
					return
				}

				if inWrite.Add(1) > 1 {
					overlapped.Store(true)
				}

				runtime.Gosched()
				inWrite.Add(-1)
				rel()
			}
		}()
	}

	wg.Wait()

	if overlapped.Load() {
		t.Fatal("two writes held the limiter at once")
	}
}

func TestReadConcurrencyCap(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	l := ForAccount(testKey("read-cap"))
	l.Configure(0, 2)

	ctx := context.Background()

	rel1, _, err := l.Acquire(ctx, Read)
	if err != nil {
		t.Fatal(err)
	}

	rel2, _, err := l.Acquire(ctx, Read)
	if err != nil {
		t.Fatal(err)
	}

	// A third read must block on the cap; cancelling its context must
	// fail it without leaking anything.
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		_, _, acqErr := l.Acquire(cctx, Read)
		done <- acqErr
	}()

	select {
	case gotErr := <-done:
		t.Fatalf("third read was granted past the cap (err=%v)", gotErr)
	case <-time.After(30 * time.Millisecond):
	}

	cancel()

	if doneErr := <-done; !errors.Is(doneErr, context.Canceled) {
		t.Fatalf("cancelled acquire returned %v, want context.Canceled", doneErr)
	}

	// Releasing one slot must let a new read through.
	rel1()

	rel3, _, err := l.Acquire(ctx, Read)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	rel3()
	rel2()

	l.reads.mu.Lock()
	active := l.reads.active
	l.reads.mu.Unlock()

	if active != 0 {
		t.Fatalf("read slots leaked: active=%d, want 0", active)
	}
}

func TestAcquireCtxAlreadyCancelled(t *testing.T) {
	l := ForAccount(testKey("ctx-precancelled"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := l.Acquire(ctx, Read); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestAcquireCtxCancelDuringBucketWait(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	installNow(t, func() time.Time { return fixed })
	// sleep blocks until the context is cancelled: time never advances,
	// so the second acquire is pinned inside the bucket wait.
	installSleep(t, func(ctx context.Context, d time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	})

	l := ForAccount(testKey("ctx-bucket-wait"))
	ctx := context.Background()

	rel1, _, err := l.Acquire(ctx, Read)
	if err != nil {
		t.Fatal(err)
	}

	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		_, _, acqErr := l.Acquire(cctx, Read)
		done <- acqErr
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	if doneErr := <-done; !errors.Is(doneErr, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", doneErr)
	}

	// The failed acquire must not leak its read slot.
	l.reads.mu.Lock()
	active := l.reads.active
	l.reads.mu.Unlock()

	if active != 1 {
		t.Fatalf("active=%d after cancelled bucket wait, want 1 (only the held slot)", active)
	}

	rel1()
	l.reads.mu.Lock()
	active = l.reads.active
	l.reads.mu.Unlock()

	if active != 0 {
		t.Fatalf("active=%d after release, want 0", active)
	}
}

func TestReleaseIdempotent(t *testing.T) {
	fc := newFakeClock()
	installClock(t, fc)

	l := ForAccount(testKey("release-idempotent"))

	rel, _, err := l.Acquire(context.Background(), Read)
	if err != nil {
		t.Fatal(err)
	}

	rel()
	rel()
	l.reads.mu.Lock()
	active := l.reads.active
	l.reads.mu.Unlock()

	if active != 0 {
		t.Fatalf("double release corrupted the slot count: active=%d", active)
	}
}

// --- Configure ------------------------------------------------------------

func (l *Limiter) snapshot() (interval time.Duration, readLimit int) {
	l.mu.Lock()
	interval = l.interval
	l.mu.Unlock()
	l.reads.mu.Lock()
	readLimit = l.reads.limit
	l.reads.mu.Unlock()

	return interval, readLimit
}

func TestConfigureDefaults(t *testing.T) {
	l := ForAccount(testKey("configure-defaults"))

	iv, rc := l.snapshot()
	if iv != 125*time.Millisecond {
		t.Fatalf("default interval %v, want 125ms (8 rps)", iv)
	}

	if rc != 4 {
		t.Fatalf("default read concurrency %d, want 4", rc)
	}
}

func TestConfigureFirstWins(t *testing.T) {
	l := ForAccount(testKey("configure-first"))
	// The first explicit call wins even when looser than the defaults.
	l.Configure(10, 8)

	iv, rc := l.snapshot()
	if iv != 100*time.Millisecond {
		t.Fatalf("interval %v after Configure(10, 8), want 100ms", iv)
	}

	if rc != 8 {
		t.Fatalf("read concurrency %d after Configure(10, 8), want 8", rc)
	}
}

func TestConfigureTightenOnly(t *testing.T) {
	l := ForAccount(testKey("configure-tighten"))
	l.Configure(10, 8)

	// Looser values are ignored.
	l.Configure(12, 16)

	iv, rc := l.snapshot()
	if iv != 100*time.Millisecond || rc != 8 {
		t.Fatalf("loosening applied: interval=%v readConcurrency=%d", iv, rc)
	}

	// Tighter values are applied.
	l.Configure(5, 2)

	iv, rc = l.snapshot()
	if iv != 200*time.Millisecond {
		t.Fatalf("interval %v after tightening to 5 rps, want 200ms", iv)
	}

	if rc != 2 {
		t.Fatalf("read concurrency %d after tightening to 2, want 2", rc)
	}

	// And cannot be loosened back.
	l.Configure(8, 4)

	iv, rc = l.snapshot()
	if iv != 200*time.Millisecond || rc != 2 {
		t.Fatalf("re-loosening applied: interval=%v readConcurrency=%d", iv, rc)
	}
}

func TestConfigureNonPositiveIgnored(t *testing.T) {
	l := ForAccount(testKey("configure-nonpositive"))
	l.Configure(0, 0)
	l.Configure(-1, -1)

	iv, rc := l.snapshot()
	if iv != 125*time.Millisecond || rc != 4 {
		t.Fatalf("non-positive values changed config: interval=%v readConcurrency=%d", iv, rc)
	}
	// They also do not consume the first-call-wins slot.
	l.Configure(9, 5)
	iv, rc = l.snapshot()

	rps := 9.0
	if want := time.Duration(float64(time.Second) / rps); iv != want {
		t.Fatalf("interval %v after first real Configure, want %v", iv, want)
	}

	if rc != 5 {
		t.Fatalf("read concurrency %d after first real Configure, want 5", rc)
	}
}

// --- Policy.Wait ----------------------------------------------------------

func TestWaitExhausted(t *testing.T) {
	p := DefaultPolicy()
	for _, attempt := range []int{3, 4, 10} {
		if _, err := p.Wait(attempt, 0); !errors.Is(err, ErrExhausted) {
			t.Fatalf("Wait(%d, 0) err = %v, want ErrExhausted", attempt, err)
		}
	}

	if _, err := p.Wait(2, 0); err != nil {
		t.Fatalf("Wait(2, 0) err = %v, want nil (one retry left)", err)
	}
}

func TestWaitRetryAfterHonouredExactly(t *testing.T) {
	installRand(t, func() float64 {
		t.Error("jitter seam consulted for a Retry-After wait")
		return 0
	})

	p := DefaultPolicy()
	for _, ra := range []time.Duration{time.Second, 5 * time.Second, 60 * time.Second} {
		d, err := p.Wait(1, ra)
		if err != nil {
			t.Fatalf("Wait(1, %v): %v", ra, err)
		}

		if d != ra {
			t.Fatalf("Wait(1, %v) = %v, want the server value exactly", ra, d)
		}
	}
}

func TestWaitRetryAfterOverCeiling(t *testing.T) {
	p := DefaultPolicy()

	_, err := p.Wait(1, 61*time.Second)
	if err == nil {
		t.Fatal("Wait accepted a Retry-After above the ceiling")
	}

	var rae *RetryAfterError
	if !errors.As(err, &rae) {
		t.Fatalf("err = %T %v, want *RetryAfterError", err, err)
	}

	if rae.RetryAfter != 61*time.Second {
		t.Fatalf("RetryAfterError carries %v, want the server's 61s", rae.RetryAfter)
	}

	if rae.Ceiling != 60*time.Second {
		t.Fatalf("RetryAfterError ceiling %v, want 60s", rae.Ceiling)
	}
	// Exhaustion and over-ceiling are distinct failures.
	if errors.Is(err, ErrExhausted) {
		t.Fatal("over-ceiling error must not read as exhaustion")
	}
}

func TestWaitJitterBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic jitter-distribution test, not cryptography
	installRand(t, rng.Float64)

	p := Policy{
		Base:              500 * time.Millisecond,
		Cap:               8 * time.Second,
		RetryAfterCeiling: time.Minute,
		Multiplier:        2,
		MaxAttempts:       100,
	}

	cases := []struct {
		attempt int
		bound   time.Duration
	}{
		{1, 500 * time.Millisecond},
		{2, time.Second},
		{3, 2 * time.Second},
		{10, 8 * time.Second}, // capped
	}
	for _, c := range cases {
		var maxSeen time.Duration

		for i := 0; i < 1000; i++ {
			d, err := p.Wait(c.attempt, 0)
			if err != nil {
				t.Fatalf("Wait(%d, 0): %v", c.attempt, err)
			}

			if d < 0 || d >= c.bound {
				t.Fatalf("Wait(%d, 0) = %v, want in [0, %v)", c.attempt, d, c.bound)
			}

			if d > maxSeen {
				maxSeen = d
			}
		}

		if maxSeen < c.bound/2 {
			t.Fatalf(
				"attempt %d: 1000 samples never exceeded %v of bound %v; jitter not uniform",
				c.attempt,
				maxSeen,
				c.bound,
			)
		}
	}
}

func TestWaitBackoffGrowth(t *testing.T) {
	// randFloat pinned to 1 makes Wait return the backoff envelope
	// itself, exposing the exponential curve and its cap.
	installRand(t, func() float64 { return 1 })

	p := Policy{
		Base:              500 * time.Millisecond,
		Cap:               8 * time.Second,
		RetryAfterCeiling: time.Minute,
		Multiplier:        2,
		MaxAttempts:       100,
	}

	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second, // capped
		8 * time.Second,
	}
	for i, w := range want {
		d, err := p.Wait(i+1, 0)
		if err != nil {
			t.Fatalf("Wait(%d, 0): %v", i+1, err)
		}

		if d != w {
			t.Fatalf("Wait(%d, 0) envelope = %v, want %v", i+1, d, w)
		}
	}
}

// --- ParseRetryAfter ------------------------------------------------------

func TestParseRetryAfter(t *testing.T) {
	fixed := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	installNow(t, func() time.Time { return fixed })

	cases := []struct {
		header string
		want   time.Duration
	}{
		{"5", 5 * time.Second},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"-3", 0},
		{"", 0},
		{"garbage", 0},
		{"5.5", 0},
		{fixed.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{fixed.Add(-10 * time.Second).Format(http.TimeFormat), 0},
		{fixed.Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := ParseRetryAfter(c.header); got != c.want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

// --- Breaker --------------------------------------------------------------

func TestBreakerOpensAfterTwoFailuresForever(t *testing.T) {
	b := BreakerFor(testKey("breaker-basic"))
	if err := b.Allow(); err != nil {
		t.Fatalf("fresh breaker refused: %v", err)
	}

	b.RecordAuthFailure()

	if err := b.Allow(); err != nil {
		t.Fatalf("breaker refused after one failure: %v", err)
	}

	b.RecordAuthFailure()

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("breaker after two failures returned %v, want ErrOpen", err)
	}
	// Open is permanent: more failures or more calls never close it.
	for i := 0; i < 100; i++ {
		if err := b.Allow(); !errors.Is(err, ErrOpen) {
			t.Fatalf("breaker closed again on call %d: %v", i, err)
		}
	}

	b.RecordAuthFailure()

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatal("breaker closed after a third failure")
	}
}

func TestBreakerForIdentityAndCase(t *testing.T) {
	lower, upper := caseVariedKeys("breaker-case")
	a := BreakerFor(upper)

	b := BreakerFor(lower)
	if a != b {
		t.Fatal("case-varied Host produced different breakers")
	}

	c := BreakerFor(testKey("breaker-other"))
	if a == c {
		t.Fatal("distinct keys shared a breaker")
	}
	// Sharing is behavioural too: failures recorded via one handle open
	// the breaker seen through the other.
	a.RecordAuthFailure()
	a.RecordAuthFailure()

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatal("failures recorded on one handle did not open the shared breaker")
	}
}

func TestBreakerRace(t *testing.T) {
	b := BreakerFor(testKey("breaker-race"))

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				b.RecordAuthFailure()
				_ = b.Allow()
			}
		}()
	}

	wg.Wait()

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("breaker not open after concurrent failures: %v", err)
	}
}
