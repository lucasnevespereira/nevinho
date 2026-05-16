package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecContextOfDefaultsToInteractive(t *testing.T) {
	ec := ExecContextOf(context.Background())
	if ec.Source != SourceInteractive {
		t.Fatalf("default source = %q, want %q", ec.Source, SourceInteractive)
	}
	for _, cap := range []Capability{CapNetRead, CapFSRead, CapFSWrite, CapShellExec, CapScheduleMut} {
		if !ec.Caps.Has(cap) {
			t.Errorf("interactive default missing %q", cap)
		}
	}
}

func TestWithExecContextRoundTrip(t *testing.T) {
	want := ExecContext{
		Source: SourceScheduled,
		Caps:   DefaultCaps[SourceScheduled],
		UserID: "scheduler:abc",
	}
	ctx := WithExecContext(context.Background(), want)
	got := ExecContextOf(ctx)

	if got.Source != want.Source || got.UserID != want.UserID {
		t.Fatalf("round trip lost fields: %+v vs %+v", got, want)
	}
	if got.Caps.Has(CapShellExec) {
		t.Error("scheduled context must not grant shell.exec")
	}
	if !got.Caps.Has(CapNetRead) {
		t.Error("scheduled context must grant net.read")
	}
}

func TestScheduledContextHidesShellAndWriteTools(t *testing.T) {
	r, _ := newTestRegistry(t)

	ctx := WithExecContext(context.Background(), ExecContext{
		Source: SourceScheduled,
		Caps:   DefaultCaps[SourceScheduled],
	})
	defs := r.DefsFor(ctx)

	got := make(map[string]bool, len(defs))
	for _, d := range defs {
		got[d.Name] = true
	}

	mustHave := []string{"web_read", "web_search", "file_read", "file_list", "grep", "find"}
	mustHide := []string{"bash", "file_write", "file_edit", "schedule"}

	for _, name := range mustHave {
		if !got[name] {
			t.Errorf("scheduled DefsFor missing read-only tool %q", name)
		}
	}
	for _, name := range mustHide {
		if got[name] {
			t.Errorf("scheduled DefsFor leaks privileged tool %q", name)
		}
	}
}

func TestInteractiveContextExposesEveryTool(t *testing.T) {
	r, _ := newTestRegistry(t)

	defs := r.DefsFor(context.Background()) // default = interactive
	if len(defs) != len(r.Defs()) {
		t.Fatalf("interactive DefsFor returned %d defs, want all %d", len(defs), len(r.Defs()))
	}
}

func TestExecuteBlocksOnMissingCapability(t *testing.T) {
	r, _ := newTestRegistry(t)

	ctx := WithExecContext(context.Background(), ExecContext{
		Source: SourceScheduled,
		Caps:   DefaultCaps[SourceScheduled],
	})
	input, _ := json.Marshal(map[string]any{"command": "echo hi"})
	out := r.Execute(ctx, "bash", input, "scheduler:abc")

	if !strings.HasPrefix(out, "blocked:") {
		t.Fatalf("expected capability block, got: %q", out)
	}
	if !strings.Contains(out, string(CapShellExec)) {
		t.Errorf("block message should name the missing capability, got: %q", out)
	}
}
