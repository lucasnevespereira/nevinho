package schedule

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/logger"
)

// RunFunc executes a schedule prompt and returns the agent's reply, or an
// error. It receives a derived context with RunTimeout and a userID
// namespace ("scheduler:<id>") so manual chat history is not polluted.
type RunFunc func(ctx context.Context, userID, prompt string) (string, error)

// NotifyFunc delivers a run result to the user. The runner calls it after
// each completion (success or error). Errors arrive with err set and a
// human readable message in result.
type NotifyFunc func(schedule Schedule, result string, err error)

// Runner ticks once a minute, fires due schedules concurrently, and
// reports results through the notify hook. One goroutine per due
// schedule per tick. Per userID locking in the agent prevents the same
// schedule from running twice in parallel.
type Runner struct {
	store  *Store
	run    RunFunc
	notify NotifyFunc

	tickInterval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running bool
}

// NewRunner wires the runner to its store and the agent. tickInterval
// defaults to one minute when zero.
func NewRunner(store *Store, run RunFunc, notify NotifyFunc, tickInterval time.Duration) *Runner {
	if tickInterval <= 0 {
		tickInterval = time.Minute
	}
	return &Runner{
		store:        store,
		run:          run,
		notify:       notify,
		tickInterval: tickInterval,
	}
}

// Start launches the tick loop. Calling Start twice is a no op.
func (r *Runner) Start() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.mu.Unlock()

	r.wg.Add(1)
	go r.loop()
}

// Stop cancels in-flight runs and waits for the loop and all run
// goroutines to drain. Safe to call from a signal handler.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	cancel := r.cancel
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

func (r *Runner) loop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.tickInterval)
	defer ticker.Stop()

	// First tick on entry so a recently due schedule fires before the
	// first interval elapses.
	r.tick()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

func (r *Runner) tick() {
	due := r.store.dueSchedules(time.Now())
	for _, sched := range due {
		r.wg.Add(1)
		go func(s Schedule) {
			defer r.wg.Done()
			r.execute(s)
		}(sched)
	}
}

func (r *Runner) execute(sched Schedule) {
	logger.Info(fmt.Sprintf("schedule: running %s", sched.Name))
	ranAt := time.Now()

	ctx, cancel := context.WithTimeout(r.ctx, RunTimeout)
	defer cancel()

	userID := "scheduler:" + sched.ID
	result, err := safeRun(ctx, r.run, userID, sched.Prompt)

	if updateErr := r.store.recordRun(sched.ID, ranAt); updateErr != nil {
		logger.Err(fmt.Errorf("schedule %s: persist run: %w", sched.Name, updateErr))
	}

	if r.notify != nil {
		r.notify(sched, result, err)
	}
}

// safeRun calls the agent and recovers from any panic so a single bad
// scheduled prompt cannot take down the runner.
func safeRun(ctx context.Context, run RunFunc, userID, prompt string) (result string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("scheduled run panicked: %v", rec)
		}
	}()
	return run(ctx, userID, prompt)
}
