package llm

import (
	"context"
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
