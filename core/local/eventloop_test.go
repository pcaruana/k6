package local

import (
	"io/ioutil"
	"net/url"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.k6.io/k6/js"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/lib/metrics"
	"go.k6.io/k6/lib/testutils"
	"go.k6.io/k6/lib/types"
	"go.k6.io/k6/loader"
)

func TestEventLoop(t *testing.T) {
	t.Parallel()
	script := []byte(`
		setTimeout(()=> {console.log("initcontext setTimeout")}, 200)
		console.log("initcontext");
		export default function() {
			setTimeout(()=> {console.log("default setTimeout")}, 200)
			console.log("default");
		};
		export function setup() {
			setTimeout(()=> {console.log("setup setTimeout")}, 200)
			console.log("setup");
		};
		export function teardown() {
			setTimeout(()=> {console.log("teardown setTimeout")}, 200)
			console.log("teardown");
		};
		export function handleSummary() {
			setTimeout(()=> {console.log("handleSummary setTimeout")}, 200)
			console.log("handleSummary");
		};
`)

	logger := logrus.New()
	logger.SetOutput(ioutil.Discard)
	logHook := testutils.SimpleLogrusHook{HookedLevels: []logrus.Level{logrus.InfoLevel}}
	logger.AddHook(&logHook)

	registry := metrics.NewRegistry()
	builtinMetrics := metrics.RegisterBuiltinMetrics(registry)
	runner, err := js.New(
		logger,
		&loader.SourceData{
			URL:  &url.URL{Path: "/script.js"},
			Data: script,
		},
		nil,
		lib.RuntimeOptions{},
		builtinMetrics,
		registry,
	)
	require.NoError(t, err)

	ctx, cancel, execScheduler, samples := newTestExecutionScheduler(t, runner, logger,
		lib.Options{
			TeardownTimeout: types.NullDurationFrom(time.Second),
			SetupTimeout:    types.NullDurationFrom(time.Second),
		})
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- execScheduler.Run(ctx, ctx, samples, builtinMetrics) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
		_, err = runner.HandleSummary(ctx, &lib.Summary{RootGroup: &lib.Group{}})
		require.NoError(t, err)
		entries := logHook.Drain()
		msgs := make([]string, len(entries))
		for i, entry := range entries {
			msgs[i] = entry.Message
		}
		require.Equal(t, []string{
			"initcontext", // first initialization
			"initcontext setTimeout",
			"initcontext", // for vu
			"initcontext setTimeout",
			"initcontext", // for setup
			"initcontext setTimeout",
			"setup", // setup
			"setup setTimeout",
			"default", // one iteration
			"default setTimeout",
			"initcontext", // for teardown
			"initcontext setTimeout",
			"teardown", // teardown
			"teardown setTimeout",
			"initcontext", // for handleSummary
			"initcontext setTimeout",
			"handleSummary", // handleSummary
			"handleSummary setTimeout",
		}, msgs)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

func TestEventLoopCrossScenario(t *testing.T) {
	t.Parallel()
	// TODO refactor the repeating parts here and the previous test
	script := []byte(`
import exec from "k6/execution"
export const options = {
        scenarios: {
                "first":{
                        executor: "shared-iterations",
                        maxDuration: "1s",
                        iterations: 1,
                        vus: 1,
                        gracefulStop:"1s",
                },
                "second": {
                        executor: "shared-iterations",
                        maxDuration: "1s",
                        iterations: 1,
                        vus: 1,
                        startTime: "3s",
                }
        }
}
export default function() {
	let i = exec.scenario.name
	setTimeout(()=> {console.log(i)}, 3000)
}
`)

	logger := logrus.New()
	logger.SetOutput(ioutil.Discard)
	logHook := testutils.SimpleLogrusHook{HookedLevels: []logrus.Level{logrus.ErrorLevel, logrus.WarnLevel, logrus.InfoLevel}}
	logger.AddHook(&logHook)

	registry := metrics.NewRegistry()
	builtinMetrics := metrics.RegisterBuiltinMetrics(registry)
	runner, err := js.New(
		logger,
		&loader.SourceData{
			URL:  &url.URL{Path: "/script.js"},
			Data: script,
		},
		nil,
		lib.RuntimeOptions{},
		builtinMetrics,
		registry,
	)
	require.NoError(t, err)
	options := runner.GetOptions()

	ctx, cancel, execScheduler, samples := newTestExecutionScheduler(t, runner, logger, options)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- execScheduler.Run(ctx, ctx, samples, builtinMetrics) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
		entries := logHook.Drain()
		msgs := make([]string, len(entries))
		for i, entry := range entries {
			msgs[i] = entry.Message
		}
		require.Equal(t, []string{"second"}, msgs)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

func TestEventLoopCrossIterations(t *testing.T) {
	t.Parallel()
	// TODO refactor the repeating parts here and the previous test
	script := []byte(`
import { sleep } from "k6"
export const options = {
  iterations: 2,
  vus: 1,
}

export default function() {
  let i = __ITER;
	setTimeout(()=> { console.log(i) }, 1000)
  if (__ITER == 0) {
    throw "just error"
  } else {
    sleep(1)
  }
}
`)

	logger := logrus.New()
	logger.SetOutput(ioutil.Discard)
	logHook := testutils.SimpleLogrusHook{HookedLevels: []logrus.Level{logrus.InfoLevel, logrus.WarnLevel, logrus.ErrorLevel}}
	logger.AddHook(&logHook)

	registry := metrics.NewRegistry()
	builtinMetrics := metrics.RegisterBuiltinMetrics(registry)
	runner, err := js.New(
		logger,
		&loader.SourceData{
			URL:  &url.URL{Path: "/script.js"},
			Data: script,
		},
		nil,
		lib.RuntimeOptions{},
		builtinMetrics,
		registry,
	)
	require.NoError(t, err)
	options := runner.GetOptions()

	ctx, cancel, execScheduler, samples := newTestExecutionScheduler(t, runner, logger, options)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- execScheduler.Run(ctx, ctx, samples, builtinMetrics) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
		entries := logHook.Drain()
		msgs := make([]string, len(entries))
		for i, entry := range entries {
			msgs[i] = entry.Message
		}
		require.Equal(t, []string{"just error\n\tat /script.js:12:4(13)\n\tat native\n", "1"}, msgs)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}
