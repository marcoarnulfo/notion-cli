package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRetriesOn429AndHonoursRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"code":"rate_limited","message":"slow down"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want 2", calls)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept %v, want one 2s wait from Retry-After", slept)
	}
}

func TestDoRetriesOn503WithExponentialBackoff(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if len(slept) != 2 || slept[1] <= slept[0] {
		t.Fatalf("backoff did not grow: %v", slept)
	}
}

// 529 is service_overload. Notion documents it next to 429 and asks for the
// same treatment, and it has no net/http constant, so it is easy to miss.
func TestDoRetriesOn529(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(529)
			w.Write([]byte(`{"code":"service_overload","message":"overloaded"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want 2", calls)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("slept %v, want one 1s wait from Retry-After", slept)
	}
}

func TestDoGivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":"rate_limited","message":"nope"}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithMaxRetries(2), WithSleep(func(time.Duration) {}))
	err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil)

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
	if calls != 3 { // first attempt + 2 retries
		t.Fatalf("made %d calls, want 3", calls)
	}
}

// A 400 is the caller's fault: retrying it wastes the shared rate budget.
func TestDoDoesNotRetryClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"validation_error","message":"bad"}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithSleep(func(time.Duration) {}))
	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("made %d calls, want 1", calls)
	}
}

// A negative retry budget used to make the loop body run zero times, so do()
// returned nil having never reached the network — a silent success.
func TestNegativeMaxRetriesStillPerformsOneAttempt(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithMaxRetries(-1), WithSleep(func(time.Duration) {}))
	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("made %d calls, want 1", calls)
	}
}
