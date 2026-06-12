package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{529, true},
		{400, false},
		{401, false},
		{402, false},
		{404, false},
		{200, false},
	}

	for _, tt := range tests {
		if got := isRetryable(tt.code); got != tt.want {
			t.Errorf("isRetryable(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
	}

	for _, tt := range tests {
		if got := backoffDelay(tt.attempt); got != tt.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestSleepWithContext_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	ok := sleepWithContext(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if ok {
		t.Error("expected false for cancelled context")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("took %v, should return immediately on cancelled context", elapsed)
	}
}

func TestDoSSERetriesRetryableStatus(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
	}))
	defer srv.Close()

	var got string
	err := doSSE(context.Background(), srv.URL, map[string]string{"hello": "world"}, nil, func(data []byte) error {
		got = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
	if got != `{"ok":true}` {
		t.Fatalf("got data %q", got)
	}
}

func TestDoSSEDoesNotRetryNonRetryableStatus(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	err := doSSE(context.Background(), srv.URL, map[string]string{"hello": "world"}, nil, func(data []byte) error {
		t.Fatalf("unexpected data: %s", data)
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}
