package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bingyuegong/go-ios/ios/testmanagerd"
)

func runTestCommand(ctx commandContext) {
	bundleID, _ := ctx.Args.String("--bundle-id")
	testRunnerBundleId, _ := ctx.Args.String("--test-runner-bundle-id")
	xctestConfig, _ := ctx.Args.String("--xctest-config")

	testsToRunArg := ctx.Args["--test-to-run"]
	var testsToRun []string
	if testsToRunArg != nil && len(testsToRunArg.([]string)) > 0 {
		testsToRun = testsToRunArg.([]string)
	}

	testsToSkipArg := ctx.Args["--test-to-skip"]
	var testsToSkip []string
	if testsToSkipArg != nil && len(testsToSkipArg.([]string)) > 0 {
		testsToSkip = testsToSkipArg.([]string)
	}

	rawTestlog, rawTestlogErr := ctx.Args.String("--log-output")
	envRaw, _ := ctx.Args["<e>"].([]string)
	if envRaw == nil {
		envRaw = []string{}
	}
	env := splitKeyValuePairs(envRaw, "=")
	isXCTest, _ := ctx.Args.Bool("--xctest")

	config := testmanagerd.TestConfig{
		BundleId:           bundleID,
		TestRunnerBundleId: testRunnerBundleId,
		XctestConfigName:   xctestConfig,
		Env:                env,
		TestsToRun:         testsToRun,
		TestsToSkip:        testsToSkip,
		XcTest:             isXCTest,
		Device:             ctx.Device,
	}

	if rawTestlogErr == nil {
		var writer *os.File = os.Stdout
		if rawTestlog != "-" {
			file, err := os.Create(rawTestlog)
			exitIfError("Cannot open file "+rawTestlog, err)
			writer = file
		}
		defer writer.Close()

		config.Listener = testmanagerd.NewTestListener(writer, writer, os.TempDir())

		testResults, err := testmanagerd.RunTestWithConfig(context.TODO(), config)
		if err != nil {
			slog.Info("Failed running Xcuitest", "error", err)
		}

		fmt.Println(convertToJSONString(testResults))
	} else {
		config.Listener = testmanagerd.NewTestListener(io.Discard, io.Discard, os.TempDir())
		testResults, err := testmanagerd.RunTestWithConfig(context.TODO(), config)
		if err != nil {
			slog.Info("Failed running Xcuitest", "error", err)
		}

		fmt.Println(convertToJSONString(testResults))
	}
}

func runXCTestCommand(ctx commandContext) {
	xctestrunFilePath, _ := ctx.Args.String("--xctestrun-file-path")

	rawTestlog, rawTestlogErr := ctx.Args.String("--log-output")

	if rawTestlogErr == nil {
		var writer *os.File = os.Stdout
		if rawTestlog != "-" {
			file, err := os.Create(rawTestlog)
			exitIfError("Cannot open file "+rawTestlog, err)
			writer = file
		}
		defer writer.Close()
		listener := testmanagerd.NewTestListener(writer, writer, os.TempDir())

		testResults, err := testmanagerd.StartXCTestWithConfig(context.TODO(), xctestrunFilePath, ctx.Device, listener)
		if err != nil {
			slog.Info("Failed running Xctest", "error", err)
		}

		fmt.Println(convertToJSONString(testResults))
	} else {
		listener := testmanagerd.NewTestListener(io.Discard, io.Discard, os.TempDir())
		testResults, err := testmanagerd.StartXCTestWithConfig(context.TODO(), xctestrunFilePath, ctx.Device, listener)
		if err != nil {
			slog.Info("Failed running Xctest", "error", err)
		}

		fmt.Println(convertToJSONString(testResults))
	}
}

func runWDACommand(ctx commandContext) {
	bundleID, _ := ctx.Args.String("--bundleid")
	testbundleID, _ := ctx.Args.String("--testrunnerbundleid")
	xctestconfig, _ := ctx.Args.String("--xctestconfig")
	wdaargsRaw, _ := ctx.Args["<a>"].([]string)
	if wdaargsRaw == nil {
		wdaargsRaw = []string{}
	}
	wdaargs := wdaargsRaw
	wdaenvRaw, _ := ctx.Args["<e>"].([]string)
	if wdaenvRaw == nil {
		wdaenvRaw = []string{}
	}
	wdaenv := splitKeyValuePairs(wdaenvRaw, "=")

	// Where XCTest activity attachments (e.g. screen recordings started via WDA's
	// /wda/screenrecord/start, see docs/ScreenRecording-XCTestNative-Plan.md in the WDA-26.6
	// repo) are written to disk as they are observed over DTX. Defaults to the OS temp
	// directory, preserving the previous hardcoded behavior, when not explicitly set.
	//
	// NOTE: the value is read from the "<attachmentsdir>" placeholder key, not
	// "--attachments-dir" -- verified directly against docopt-go's parsed Opts map for this
	// exact usage grammar: "--attachments-dir" has NO entry in the "Options:" section below
	// (unlike "--log-output", which IS declared there as "--log-output <file>  Log output
	// file."), so docopt-go never registers it as a formal Option object. Instead it falls
	// back to matching the bare placeholder "<attachmentsdir>" as an independent positional
	// Argument node, and "-attachments-dir"/"--attachments-dir" itself only ends up as an
	// unrelated boolean presence flag (value `true`), NOT the string value. This is the
	// opposite of "--log-output <file>", which DOES have an "Options:" entry and therefore
	// stores its value under the option's own canonical key "--log-output" -- NOT under
	// "<file>" as an earlier version of this comment incorrectly claimed (that claim was
	// never actually verified against a real docopt.Opts dump; see the "-log-output" fix
	// applied to this function and to runTestCommand/runXCTestCommand above, once dumping
	// ctx.Args for a real invocation showed "<file>" is never populated and "--log-output"
	// is). Bottom line: whether a docopt option's value lands under "--optionname" or under
	// "<placeholder>" depends entirely on whether that option has an "Options:" section
	// entry, not on any pattern involving the placeholder name -- there is no shortcut here,
	// every new option added to this CLI must be checked by actually dumping ctx.Args once.
	attachmentsDir, attachmentsDirErr := ctx.Args.String("<attachmentsdir>")
	if attachmentsDirErr != nil || attachmentsDir == "" {
		attachmentsDir = os.TempDir()
	}
	slog.Info("screen recording / activity attachments directory", "path", attachmentsDir)

	if bundleID == "" && testbundleID == "" && xctestconfig == "" {
		slog.Info("no bundle ids specified, falling back to defaults")
		bundleID, testbundleID, xctestconfig = "com.facebook.WebDriverAgentRunner.xctrunner", "com.facebook.WebDriverAgentRunner.xctrunner", "WebDriverAgentRunner.xctest"
	}
	if bundleID == "" || testbundleID == "" || xctestconfig == "" {
		slog.Error("please specify either NONE of bundleid, testbundleid and xctestconfig or ALL of them. At least one was empty.", "bundleid", bundleID, "testbundleid", testbundleID, "xctestconfig", xctestconfig)
		return
	}
	slog.Info("Running wda", "bundleid", bundleID, "testbundleid", testbundleID, "xctestconfig", xctestconfig)

	rawTestlog, rawTestlogErr := ctx.Args.String("--log-output")

	var writer io.Writer

	if rawTestlogErr == nil {
		writerCloser := os.Stdout
		writer = writerCloser
		if rawTestlog != "-" {
			file, err := os.Create(rawTestlog)
			exitIfError("Cannot open file "+rawTestlog, err)
			writer = file
		}
		defer writerCloser.Close()
	} else {
		writer = io.Discard
	}

	errorChannel := make(chan error)
	defer close(errorChannel)
	ctxWDA, stopWda := context.WithCancel(context.Background())
	go func() {
		testResults, err := testmanagerd.RunTestWithConfig(ctxWDA, testmanagerd.TestConfig{
			BundleId:           bundleID,
			TestRunnerBundleId: testbundleID,
			XctestConfigName:   xctestconfig,
			Env:                wdaenv,
			Args:               wdaargs,
			Device:             ctx.Device,
			Listener:           testmanagerd.NewTestListener(writer, writer, attachmentsDir),
		})
		// DIAG (read-only, does not affect control flow): testResults was previously
		// discarded via `_, err :=` above. This is the ONLY place that exposes the real
		// pass/fail/stalled status testmanagerd reports back for the WDA test case (e.g.
		// "testRunner") once RunTestWithConfig returns -- which happens after /wda/shutdown
		// causes the never-ending test method to return, or after a signal/crash. Logging it
		// answers a concrete open question: does a clean /wda/shutdown actually get reported
		// by testmanagerd as TestCaseStatus "passed", or something else (e.g. "stalled")?
		// See testlistener.go's TestCaseStatus constants for the possible values.
		for _, suite := range testResults {
			for _, tc := range suite.TestCases {
				slog.Info("wda test case finished", "class", tc.ClassName, "method", tc.MethodName, "status", tc.Status, "duration", tc.Duration)
			}
		}
		if err != nil {
			errorChannel <- err
		}
		stopWda()
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errorChannel:
		slog.Error("Failed running WDA", "error", err)
		stopWda()
		os.Exit(1)
	case <-ctxWDA.Done():
		slog.Error("WDA process ended unexpectedly")
		os.Exit(1)
	case signal := <-c:
		slog.Info(fmt.Sprintf("os signal %d received, closing...", signal))
		stopWda()
	}
	slog.Info("Done Closing")
}
