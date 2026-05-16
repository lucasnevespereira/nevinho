package tools

import "context"

// Capability is a coarse permission a tool needs to run. Tools tag
// themselves with one. Execution contexts grant a set. The registry
// only dispatches a tool whose capability is in the granting set.
type Capability string

const (
	CapNetRead     Capability = "net.read"
	CapFSRead      Capability = "fs.read"
	CapFSWrite     Capability = "fs.write"
	CapShellExec   Capability = "shell.exec"
	CapScheduleMut Capability = "schedule.mut"
)

// Source describes where a turn originated. Each source maps to a
// default capability preset via DefaultCaps.
type Source string

const (
	SourceInteractive Source = "interactive"
	SourceScheduled   Source = "scheduled"
)

// CapSet is an unordered set of capabilities. The zero value behaves as
// the empty set, so checking with Has on a nil set is safe.
type CapSet map[Capability]struct{}

// NewCapSet builds a set from the given capabilities.
func NewCapSet(caps ...Capability) CapSet {
	s := make(CapSet, len(caps))
	for _, c := range caps {
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether the set contains c.
func (s CapSet) Has(c Capability) bool {
	_, ok := s[c]
	return ok
}

// DefaultCaps maps each Source to its capability preset. Scheduled
// runs are read-only by design: no shell, no writes, no schedule
// mutation (so a fire cannot create more schedules).
var DefaultCaps = map[Source]CapSet{
	SourceInteractive: NewCapSet(CapNetRead, CapFSRead, CapFSWrite, CapShellExec, CapScheduleMut),
	SourceScheduled:   NewCapSet(CapNetRead, CapFSRead),
}

// ExecContext travels with a turn through context.Context. Tools and
// the registry read it to decide what is permitted.
type ExecContext struct {
	Source Source
	Caps   CapSet
	UserID string
}

type execContextKey struct{}

// WithExecContext attaches ec to ctx so downstream tools and the
// registry can read it via ExecContextOf.
func WithExecContext(ctx context.Context, ec ExecContext) context.Context {
	return context.WithValue(ctx, execContextKey{}, ec)
}

// ExecContextOf returns the ExecContext attached to ctx. When none is
// attached it returns the interactive preset, which keeps existing
// direct callers (tests, ad-hoc tool invocations) working without
// having to know about contexts.
func ExecContextOf(ctx context.Context) ExecContext {
	if ec, ok := ctx.Value(execContextKey{}).(ExecContext); ok {
		return ec
	}
	return ExecContext{
		Source: SourceInteractive,
		Caps:   DefaultCaps[SourceInteractive],
	}
}
