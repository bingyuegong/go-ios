package main

import (
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/docopt/docopt-go"
)

type commandContext struct {
	Args   docopt.Opts
	Device ios.DeviceEntry
}

type command struct {
	name  string
	match func(docopt.Opts) bool
	run   func(commandContext)
}

func boolArg(args docopt.Opts, name string) bool {
	value, _ := args.Bool(name)
	return value
}

func commandByBool(name string, run func(commandContext)) command {
	return command{
		name: name,
		match: func(args docopt.Opts) bool {
			return boolArg(args, name)
		},
		run: run,
	}
}

func dispatchCommand(ctx commandContext, commands []command) bool {
	for _, cmd := range commands {
		if cmd.match(ctx.Args) {
			started := time.Now()
			diagnosticInfo("command dispatch started", "command", cmd.name, "udid", ctx.Device.Properties.SerialNumber)
			cmd.run(ctx)
			diagnosticInfo("command dispatch completed", "command", cmd.name, "udid", ctx.Device.Properties.SerialNumber, "duration_ms", time.Since(started).Milliseconds())
			return true
		}
	}
	return false
}
