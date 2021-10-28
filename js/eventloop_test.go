package js

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBasicEventLoop(t *testing.T) {
	t.Parallel()
	loop := newEventLoop()
	var ran int
	f := func() error { //nolint:unparam
		ran++
		return nil
	}
	require.NoError(t, loop.start(f))
	require.Equal(t, 1, ran)
	require.NoError(t, loop.start(f))
	require.Equal(t, 2, ran)
	require.Error(t, loop.start(func() error {
		_ = f()
		loop.reserve()(f)
		return errors.New("something")
	}))
	require.Equal(t, 3, ran)
}

func TestEventLoopReserve(t *testing.T) {
	t.Parallel()
	loop := newEventLoop()
	var ran int
	f := func() error {
		ran++
		r := loop.reserve()
		go func() {
			time.Sleep(time.Second)
			r(func() error {
				ran++
				return nil
			})
		}()
		return nil
	}
	start := time.Now()
	require.NoError(t, loop.start(f))
	took := time.Since(start)
	require.Equal(t, 2, ran)
	require.Less(t, time.Second, took)
	require.Greater(t, time.Second+time.Millisecond*100, took)
}

func TestEventLoopWaitOnReserved(t *testing.T) {
	t.Parallel()
	loop := newEventLoop()
	var ran int
	f := func() error {
		ran++
		r := loop.reserve()
		go func() {
			time.Sleep(time.Second)
			r(func() error {
				ran++
				return nil
			})
		}()
		return fmt.Errorf("expected")
	}
	start := time.Now()
	require.Error(t, loop.start(f))
	took := time.Since(start)
	loop.waitOnReserved()
	took2 := time.Since(start)
	require.Equal(t, 1, ran)
	require.Greater(t, time.Millisecond*50, took)
	require.Less(t, time.Second, took2)
	require.Greater(t, time.Second+time.Millisecond*100, took2)
}
