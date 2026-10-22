package agent

import (
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

func TestStatusCacheMetrics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage llm.Usage
		want  string
	}{
		{"no usage", llm.Usage{}, "Cache (process): no cache tokens reported"},
		{"uncached input", llm.Usage{In: 1000}, "Cache (process): no cache tokens reported"},
		{"writes only", llm.Usage{CacheWrite: 100}, "Cache (process): 0 read · 100 written · 0.0% reuse"},
		{"reads only", llm.Usage{CacheRead: 100}, "Cache (process): 100 read · 0 written · 100.0% reuse"},
		{"mixed", llm.Usage{In: 5000, CacheRead: 900, CacheWrite: 100}, "Cache (process): 900 read · 100 written · 90.0% reuse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			a := New(&fallbackProvider{}, cfg, "test", "", ModeLocal)
			a.addUsage(tc.usage)
			got := a.Status()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("missing %q in status:\n%s", tc.want, got)
			}
			if strings.Contains(got, "NaN") || strings.Contains(got, "+Inf") {
				t.Fatalf("invalid cache ratio: %s", got)
			}
			if tc.usage.CacheRead+tc.usage.CacheWrite > 0 && !strings.Contains(got, "read / (read + written)") {
				t.Fatalf("missing ratio definition: %s", got)
			}
		})
	}
}

func TestStatusCacheMetricsAccumulateAcrossTurns(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(&fallbackProvider{}, cfg, "test", "", ModeLocal)
	a.addUsage(llm.Usage{CacheWrite: 100})
	a.addUsage(llm.Usage{CacheRead: 300})
	if got := a.Status(); !strings.Contains(got, "300 read · 100 written · 75.0% reuse") {
		t.Fatalf("missing cumulative cache metrics: %s", got)
	}
}
