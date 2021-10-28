package js

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// an event loop
type eventLoop struct {
	lock          sync.Mutex
	queue         []func() error
	wakeupCh      chan struct{} // maybe use sync.Cond ?
	reservedCount int

	// a map of promises which were rejected with no handler
	// if there is something in this map at an end of a eventLoop
	// the loop will exit with an error. This is a kin to what deno and node do
	pendingPromiseRejections map[*goja.Promise]struct{}
}

func newEventLoop() *eventLoop {
	return &eventLoop{
		wakeupCh: make(chan struct{}, 1),
	}
}

func (e *eventLoop) wakeup() {
	select {
	case e.wakeupCh <- struct{}{}:
	default:
	}
}

// reserve "reserves" a spot on the loop, preventing it from returning/finishing. The returning function will queue it's
// argument and wakeup the loop if needed.
// If the eventLoop has since stopped it will not be execeted
// This function *must* be called from within running on the event loop, but it's result can be called from anywhere
// TODO better name
func (e *eventLoop) reserve() func(func() error) {
	e.lock.Lock()
	e.reservedCount++
	e.lock.Unlock()

	return func(f func() error) {
		e.lock.Lock()
		e.queue = append(e.queue, f)
		e.reservedCount--
		e.lock.Unlock()
		e.wakeup()
	}
}

func (e *eventLoop) promiseRejectionTracker(p *goja.Promise, op goja.PromiseRejectionOperation) {
	// No locking here as the goja runtime will call this synchrnously
	// Read Notes on https://tc39.es/ecma262/#sec-host-promise-rejection-tracker
	if op == goja.PromiseRejectionReject {
		e.pendingPromiseRejections[p] = struct{}{}
	} else { // goja.PromiseRejectionHandle so a promise that was previously rejected without handler now got one
		delete(e.pendingPromiseRejections, p)
	}
}

// initHelpers initializes some helpers around the runtime and the event loop
// - adding setTimeout
// - reporting (and aborting on) unhandled promise rejection
func (e *eventLoop) initHelpers(ctx context.Context, rt *goja.Runtime) {
	rt.SetPromiseRejectionTracker(e.promiseRejectionTracker)
	e.addSetTimeout(ctx, rt)
}

// start will run the event loop until it's empty and there are no reserved spots
// or a queued function returns an error. The provided function will be the first thing executed.
// After start returns the event loop should not be reused if an error was returend
func (e *eventLoop) start(f func() error) error {
	// TODO this block can be moved to newEventLoop if we are never going to reuse an event loop
	e.lock.Lock()
	e.reservedCount = 0
	e.queue = []func() error{f}
	e.pendingPromiseRejections = make(map[*goja.Promise]struct{}, len(e.pendingPromiseRejections))
	e.lock.Unlock()
	for {
		// acquire the queue
		e.lock.Lock()
		queue := e.queue
		e.queue = make([]func() error, 0, len(queue))
		reserved := e.reservedCount != 0
		e.lock.Unlock()

		if len(queue) == 0 {
			if !reserved { // we have empty queue and nothing that reserved a spot
				return nil
			}
			<-e.wakeupCh // wait until the reserved is done
			continue
		}

		for _, f := range queue {
			if err := f(); err != nil {
				return err
			}
		}

		// yes it will be at random but that seems to be the case in other tools as well
		for promise := range e.pendingPromiseRejections {
			// TODO maybe throw the whole promise up and get make a better message outside of the event loop
			value := promise.Result()
			// try to get the stack without actually needing the runtime
			// this might break in the future :(
			if o, ok := promise.Result().(*goja.Object); ok {
				stack := o.Get("stack")
				if stack != nil {
					value = stack
				}
			}
			// this is the de facto wording in both firefox and deno at least
			return fmt.Errorf("Uncaught (in promise) %s", value) //nolint:stylecheck
		}
	}
}

// this exists so we can wait on all reserved so we know nothing is still doing work
func (e *eventLoop) waitOnReserved() {
	for {
		// acquire the queue
		e.lock.Lock()
		queue := e.queue
		e.queue = make([]func() error, 0, len(queue))
		reserved := e.reservedCount != 0
		e.lock.Unlock()

		if len(queue) == 0 {
			if !reserved { // we have empty queue and nothing that reserved a spot
				return
			}
			<-e.wakeupCh // wait until the reserved is done
			continue
		}

		// don't actually do anything we just want to go through them
	}
}

func (e *eventLoop) addSetTimeout(ctx context.Context, rt *goja.Runtime) {
	_ = rt.Set("setTimeout", func(f func() error, t float64) {
		// TODO checks and fixes
		// TODO maybe really return something to use with `clearTimeout
		// TODO support arguments ... maybe
		runOnLoop := e.reserve()
		go func() {
			timer := time.NewTimer(time.Duration(t * float64(time.Millisecond)))
			select {
			case <-timer.C:
				runOnLoop(f)
			case <-ctx.Done():
				// TODO log something?

				timer.Stop()
				runOnLoop(func() error { return nil })
			}
		}()
	})
}
