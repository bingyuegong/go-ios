package main

import (
	"fmt"
	"os"

	"github.com/docopt/docopt-go"
)

var preProxyCommands = []command{
	{
		name: "version",
		match: func(args docopt.Opts) bool {
			return boolArg(args, "version") || boolArg(args, "--version")
		},
		run: func(ctx commandContext) {
			printVersion()
		},
	},
}

var globalCommands = []command{
	{
		name: "ui",
		match: func(args docopt.Opts) bool {
			// `ui install` and `ui run` need a device, so they are device
			// commands; everything else under `ui` is a URL-based client command.
			return boolArg(args, "ui") && !boolArg(args, "install") && !boolArg(args, "run")
		},
		run: runUICommand,
	},
	commandByBool("listen", runListenCommand),
	{
		name:  "list",
		match: isDeviceListCommand,
		run:   runDeviceListCommand,
	},
	{
		name: "check-port",
		match: func(args docopt.Opts) bool {
			return boolArg(args, "check-port")
		},
		run: runCheckPortCommand,
	},
	{
		// `sign certificate appstoreconnect` mints an account-wide certificate and
		// needs no device, so it is global (dispatched before device resolution).
		name: "sign certificate",
		match: func(args docopt.Opts) bool {
			return boolArg(args, "sign") && boolArg(args, "certificate")
		},
		run: runSignCertificateAppStoreConnectCommand,
	},
}

func runListenCommand(ctx commandContext) {
	startListening()
}

func runCheckPortCommand(ctx commandContext) {
	udid, _ := ctx.Args.String("-u")
	if udid == "" {
		udid = os.Getenv("GO_IOS_UDID")
	}
	if udid == "" {
		exitIfError("check-port requires -u <udid>", fmt.Errorf("-u is required"))
	}

	// 清除已断开设备的注册表记录
	pruneDisconnectedTunnelPorts()

	type checkPortResult struct {
		UDID           string `json:"udid"`
		TunnelInfoPort *int   `json:"tunnelInfoPort"`
	}

	port, ok := globalTunnelPortRegistry.Lookup(udid)
	result := checkPortResult{UDID: udid}
	if ok {
		result.TunnelInfoPort = &port
	}

	if JSONdisabled {
		if ok {
			fmt.Printf("UDID: %s\nTunnelInfoPort: %d\n", udid, port)
		} else {
			fmt.Printf("UDID: %s\nTunnelInfoPort: None\n", udid)
		}
	} else {
		fmt.Println(convertToJSONString(result))
	}
}

// isDeviceListCommand matches the bare global `ios list`. globalCommands are
// dispatched before deviceCommands, so every device subcommand that also has a
// `list` literal (`ios <cmd> list`) sets the same "list" arg and would otherwise
// be swallowed here. Each such command must be excluded below;
// TestDeviceListCommandOnlyMatchesTopLevelList guards that every `<cmd> list`
// subcommand is excluded rather than falling through to the device list.
func isDeviceListCommand(args docopt.Opts) bool {
	listCommand := boolArg(args, "list")
	diagnosticsCommand := boolArg(args, "diagnostics")
	imageCommand := boolArg(args, "image")
	deviceStateCommand := boolArg(args, "devicestate")
	profileCommand := boolArg(args, "profile")
	webInspectorCommand := boolArg(args, "webinspector")
	return listCommand && !diagnosticsCommand && !imageCommand && !deviceStateCommand && !profileCommand && !webInspectorCommand
}

func runDeviceListCommand(ctx commandContext) {
	jsonOutput, _ := ctx.Args.Bool("-J")
	printDeviceList(jsonOutput)
}
