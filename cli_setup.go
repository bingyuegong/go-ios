package main

import (
	"log/slog"
	"os"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/tunnel"
	"github.com/docopt/docopt-go"
)

func configureCLI(arguments docopt.Opts) {
	disableJSON, _ := arguments.Bool("--nojson")
	if disableJSON {
		JSONdisabled = true
	}

	pretty, _ := arguments.Bool("--pretty")
	if pretty {
		prettyJSON = true
	}

	traceLevelEnabled, _ := arguments.Bool("--trace")
	verboseLoggingEnabledLong, _ := arguments.Bool("--verbose")

	level := slog.LevelInfo
	if verboseLoggingEnabledLong {
		level = slog.LevelDebug
	}
	if traceLevelEnabled {
		level = ios.LevelTrace
	}

	handlerOpts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceTraceLevel,
	}

	var handler slog.Handler
	if disableJSON {
		handler = slog.NewTextHandler(os.Stderr, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, handlerOpts)
	}
	logger := slog.New(newExecutionCLIHandler(handler))

	slog.SetDefault(logger)
	ios.SetLogger(logger)

	if traceLevelEnabled {
		slog.Info("Set Trace mode")
	} else if verboseLoggingEnabledLong {
		slog.Info("Set Debug mode")
	}
	// The invocation log records the complete original input; detailed stages
	// and all levels of library logs go to the separate diagnostic stream.

	startAgentFromEnvironment()
}

func startAgentFromEnvironment() {
	skipAgent, _ := os.LookupEnv("ENABLE_GO_IOS_AGENT")
	if skipAgent == "user" || skipAgent == "kernel" {
		tunnel.RunAgent(skipAgent)
	}
}
