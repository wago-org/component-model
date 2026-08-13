package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/wago-org/component-model/internal/abi"
)

// This file implements CallAsync -- a host-side non-blocking counterpart to
// Instance.Call (docs/component-model-callasync-proposal.md). Where Call drives
// the async scheduler to completion on the caller's goroutine, CallAsync returns
// a PendingCall as soon as the export first parks awaiting an *external* import
// completion, and PendingCall.Await resumes driving as those completions arrive.
//
// The only new concurrency is "hop A": an AsyncHostFunc that starts real I/O and
// calls AsyncCall.Resolve later, from another goroutine, after the call returned.
// That off-driver Resolve never touches scheduler state -- it appends an
// asyncCompletion to Instance.mailbox under amu (a pure queue op that races
// nothing) and the single driver (CallAsync's initial pump, or Await) drains and
// applies it on its own goroutine. The instance is single-runnable throughout:
// exactly one goroutine ever drives, external Resolve only enqueues.
//
// This path is active only while asyncActive is set (one CallAsync outstanding).
// The instance execution lease serializes it with every synchronous/async Call.

// asyncCompletion is one queued external import completion. cancelled selects
// ResolveCancelled semantics over Resolve.
type asyncCompletion struct {
	ac        *AsyncCall
	results   []abi.Value
	cancelled bool
}

// queueExternalCompletion is the mailbox post AsyncCall.Resolve routes to while
// a CallAsync is outstanding. Pure append under amu: it never reads or writes
// subtask/sched state, so it is safe from any goroutine and races nothing the
// driver does (the driver reads the mailbox under the same lock). One-shot is
// enforced here, under the lock.
func (in *Instance) queueExternalCompletion(c asyncCompletion) error {
	in.amu.Lock()
	defer in.amu.Unlock()
	if in.pending == nil || in.pending.closed {
		c.ac.state.Store(asyncCallClosed)
		return errAsyncCallClosed
	}
	if !c.ac.state.CompareAndSwap(asyncCallOpen, asyncCallQueued) {
		return c.ac.stateError()
	}
	in.mailbox = append(in.mailbox, c)
	if in.acond != nil {
		in.acond.Signal()
	}
	return nil
}

// applyQueued applies one drained completion on the driver goroutine -- the
// exact parked-completion path buildAsyncHostWrapper's guest<->guest onResolve
// uses: applyResolve (or the cancelled resolve), then install the SUBTASK
// pending event so the next drive delivers it. Never reads ac.inCall (the
// driver may differ from the goroutine that ran the host call).
func (in *Instance) applyQueued(c asyncCompletion) error {
	st := c.ac.st
	if c.cancelled {
		if !st.cancellationRequested {
			return fmt.Errorf("component/instance: async import: ResolveCancelled without a prior subtask.cancel request")
		}
		st.resolve(subtaskCancelledBeforeReturned, nil)
	} else if err := st.applyResolve(c.results); err != nil {
		return err
	}
	if si := st.subtaski(); si != 0 {
		installSubtaskEvent(st, si)
	}
	c.ac.state.Store(asyncCallApplied)
	return nil
}

// drainMailbox applies every queued completion, on the caller's (driver)
// goroutine. Returns whether any were applied.
func (in *Instance) drainMailbox() (applied bool, err error) {
	in.amu.Lock()
	q := in.mailbox
	in.mailbox = nil
	in.amu.Unlock()
	for _, c := range q {
		if e := in.applyQueued(c); e != nil {
			return applied, e
		}
		applied = true
	}
	return applied, nil
}

func (in *Instance) registerAsyncCall(ac *AsyncCall) {
	in.amu.Lock()
	if in.pending != nil && !in.pending.closed {
		in.pending.calls = append(in.pending.calls, ac)
	} else {
		ac.state.Store(asyncCallClosed)
	}
	in.amu.Unlock()
}

// PendingCall is the handle CallAsync returns: one async export invocation in
// flight, suspended awaiting external import completions.
type PendingCall struct {
	in              *Instance
	gt              *guestTask
	t               *task
	name            string
	done            chan struct{}
	result          []abi.Value
	err             error
	closed          bool // guarded by in.amu
	lease           bool // guarded by in.amu; owns Instance.syncCallMu while true
	calls           []*AsyncCall
	driver          bool // guarded by in.amu; exactly one goroutine may pump this call
	cancelRequested bool // guarded by in.amu
}

// Done is closed when the call resolves, fails, is cancelled, or the instance
// closes. After it closes, Await returns immediately.
func (p *PendingCall) Done() <-chan struct{} { return p.done }

// CallAsync starts an async (callback) export and returns as soon as it first
// parks awaiting an external completion -- or a completed PendingCall if it
// resolves outright. Progress resumes when an outstanding AsyncHostFunc is
// Resolve()d (from any goroutine) and the caller drives it with Await.
//
// Milestone limits: one outstanding CallAsync per Instance; the export must be
// an async (callback) lift.
func (in *Instance) CallAsync(ctx context.Context, export string, args ...abi.Value) (*PendingCall, error) {
	be, ok := in.exports[export]
	if !ok {
		return nil, fmt.Errorf("component/instance: export %q not found", export)
	}
	if !be.asyncCallback {
		return nil, fmt.Errorf("component/instance: CallAsync %q: only async (callback) lifts are supported by this milestone", export)
	}
	if in.poisoned.Load() || !in.mayEnter {
		return nil, fmt.Errorf("component/instance: export %q: cannot enter component instance", export)
	}
	if len(args) != len(be.fd.Params) {
		return nil, fmt.Errorf("component/instance: export %q takes %d parameter(s), got %d", export, len(be.fd.Params), len(args))
	}
	if !in.claimAsyncExecution() {
		return nil, fmt.Errorf("component/instance: CallAsync: another call is already executing on this instance")
	}
	ctx = context.WithValue(ctx, syncCallContextKey{}, &syncCallContext{instance: in, parent: syncCallFrom(ctx)})

	fr := &callbackFrame{}
	t, gt := &fr.t, &fr.gt
	t.inst, t.be = in, be
	gt.t, gt.in, gt.be, gt.ctx, gt.exportName = t, in, be, ctx, export
	gt.args = args
	t.gt = gt

	p := &PendingCall{in: in, gt: gt, t: t, name: export, done: make(chan struct{}), lease: true}
	in.amu.Lock()
	if in.acond == nil {
		in.acond = sync.NewCond(&in.amu)
	}
	in.pending = p
	in.mailbox = nil
	in.amu.Unlock()
	in.asyncActive.Store(true)

	if err := gt.start(); err != nil {
		in.finishAsync(p, nil, err)
		in.reapParkedGoroutines()
		in.releasePendingLease(p)
		return nil, err
	}
	p.driver = true // not yet published; no competing driver can observe p
	if err := in.pumpPending(p); err != nil {
		p.releaseDriver()
		return nil, err
	}
	p.releaseDriver()
	return p, nil
}

func (p *PendingCall) claimDriver() error {
	p.in.amu.Lock()
	defer p.in.amu.Unlock()
	if p.closed {
		return nil
	}
	if p.driver {
		return errAsyncDriverActive
	}
	p.driver = true
	return nil
}

func (p *PendingCall) releaseDriver() {
	p.in.amu.Lock()
	p.driver = false
	if p.in.acond != nil {
		p.in.acond.Broadcast()
	}
	p.in.amu.Unlock()
}

// pumpPending drives the call as far as it will go right now: drive to
// park-or-done, then drain any completions queued during that drive and drive
// again, until the task is done or genuinely parked with an empty mailbox. On
// done/error it closes the handle.
func (in *Instance) pumpPending(p *PendingCall) error {
	for {
		done, err := in.sched.driveToQuiescence(func() bool { return p.gt.done })
		if err != nil {
			in.finishAsync(p, nil, err)
			in.reapParkedGoroutines()
			in.releasePendingLease(p)
			return err
		}
		if done {
			if p.gt.err != nil {
				in.finishAsync(p, nil, p.gt.err)
				in.reapParkedGoroutines()
				in.releasePendingLease(p)
				return p.gt.err
			}
			in.finishAsync(p, p.t.result, nil)
			in.releasePendingLease(p)
			return nil
		}
		applied, err := in.drainMailbox()
		if err != nil {
			in.finishAsync(p, nil, err)
			in.reapParkedGoroutines()
			in.releasePendingLease(p)
			return err
		}
		if !applied {
			return nil // parked, waiting for an external completion
		}
	}
}

// finishAsync records the terminal state once and wakes Await.
func (in *Instance) finishAsync(p *PendingCall, result []abi.Value, err error) {
	in.amu.Lock()
	if !p.closed {
		p.result, p.err, p.closed = result, err, true
		for _, ac := range p.calls {
			if ac.state.Load() != asyncCallApplied {
				ac.state.Store(asyncCallClosed)
			}
		}
		close(p.done)
	}
	if in.pending == p {
		in.pending = nil
		in.mailbox = nil
		in.asyncActive.Store(false)
	}
	if in.acond != nil {
		in.acond.Broadcast()
	}
	in.amu.Unlock()
}

func (in *Instance) releasePendingLease(p *PendingCall) {
	in.amu.Lock()
	release := p.lease
	p.lease = false
	in.amu.Unlock()
	if release {
		in.releaseAsyncExecution()
	}
}

// Await blocks until the call resolves (or ctx is done), driving the scheduler
// as external completions arrive. It is the sole driver after CallAsync returns;
// call it from a single goroutine.
func (p *PendingCall) Await(ctx context.Context) ([]abi.Value, error) {
	in := p.in
	select {
	case <-p.done:
		return p.result, p.err
	default:
	}
	if err := p.claimDriver(); err != nil {
		return nil, err
	}
	defer p.releaseDriver()

	// A watcher wakes the cond so a cancelled Await can return promptly.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			in.amu.Lock()
			if in.acond != nil {
				in.acond.Broadcast()
			}
			in.amu.Unlock()
		case <-stop:
		}
	}()

	for {
		in.amu.Lock()
		for len(in.mailbox) == 0 && !p.closed && !p.cancelRequested && ctx.Err() == nil {
			in.acond.Wait()
		}
		if p.closed {
			in.amu.Unlock()
			return p.result, p.err
		}
		if ctx.Err() != nil {
			in.amu.Unlock()
			return nil, ctx.Err()
		}
		if p.cancelRequested {
			in.amu.Unlock()
			in.finishAsync(p, nil, errCallAsyncCancelled)
			in.reapParkedGoroutines()
			in.releasePendingLease(p)
			return nil, errCallAsyncCancelled
		}
		in.amu.Unlock()

		if err := in.pumpPending(p); err != nil {
			return nil, err
		}
		select {
		case <-p.done:
			return p.result, p.err
		default:
		}
	}
}

// Cancel aborts the pending call: it stops driving, reaps any parked guest
// state, and resolves the handle with a cancellation error. This milestone does
// not deliver a graceful Component Model task.cancel to the guest.
func (p *PendingCall) Cancel(ctx context.Context) error {
	in := p.in
	in.amu.Lock()
	if p.closed {
		in.amu.Unlock()
		return nil
	}
	p.cancelRequested = true
	becomeDriver := !p.driver
	if becomeDriver {
		p.driver = true
	}
	if in.acond != nil {
		in.acond.Broadcast()
	}
	in.amu.Unlock()
	if becomeDriver {
		in.finishAsync(p, nil, errCallAsyncCancelled)
		in.reapParkedGoroutines()
		in.releasePendingLease(p)
		p.releaseDriver()
		return nil
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errCallAsyncCancelled = errors.New("component/instance: CallAsync cancelled")
var errAsyncCallClosed = errors.New("component/instance: async call is closed")
var errAsyncDriverActive = errors.New("component/instance: pending async call already has a driver")
