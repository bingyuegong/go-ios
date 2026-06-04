package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/afc"
	"github.com/danielpaulus/go-ios/ios/deviceinfo"
	"github.com/danielpaulus/go-ios/ios/diagnostics"
	"github.com/danielpaulus/go-ios/ios/instruments"
	"github.com/danielpaulus/go-ios/ios/mobileactivation"
	"github.com/danielpaulus/go-ios/ios/ostrace"
	"github.com/danielpaulus/go-ios/ios/pcap"
)

var deviceCommands = []command{
	commandByBool("activate", runActivateCommand),
	commandByBool("ip", runIPCommand),
	commandByBool("pcap", runPCAPCommand),
	commandByBool("ps", runPSCommand),
	commandByBool("install", runInstallCommand),
	commandByBool("uninstall", runUninstallCommand),
	commandByBool("lang", runLangCommand),
	commandByBool("dproxy", runDproxyCommand),
	commandByBool("info", runInfoCommand),
	commandByBool("syslog", runSyslogCommand),
	commandByBool("ostrace", runOSTraceCommand),
	commandByBool("screenshot", runScreenshotCommand),
	commandByBool("resetlocation", runResetLocationCommand),
	commandByBool("devicename", runDeviceNameCommand),
	commandByBool("apps", runAppsCommand),
	commandByBool("date", runDateCommand),
	commandByBool("diagnostics", runDiagnosticsCommand),
	commandByBool("pair", runPairCommand),
	commandByBool("readpair", runReadPairCommand),
	commandByBool("batteryregistry", runBatteryRegistryCommand),
	commandByBool("reboot", runRebootCommand),
	commandByBool("shutdown", runShutdownCommand),
	commandByBool("diskspace", runDiskspaceCommand),
	commandByBool("batterycheck", runBatteryCheckCommand),
}

func runActivateCommand(ctx commandContext) {
	exitIfError("failed activation", mobileactivation.Activate(ctx.Device))
}

func runIPCommand(ctx commandContext) {
	ip, err := pcap.FindIp(ctx.Device)
	exitIfError("failed", err)
	fmt.Println(convertToJSONString(ip))
}

func runPCAPCommand(ctx commandContext) {
	p, _ := ctx.Args.String("--process")
	i, _ := ctx.Args.Int("--pid")
	pcap.Pid = int32(i)
	pcap.ProcName = p
	err := pcap.Start(ctx.Device)
	if err != nil {
		exitIfError("pcap failed", err)
	}
}

func runPSCommand(ctx commandContext) {
	applicationsOnly, _ := ctx.Args.Bool("--apps")
	processList(ctx.Device, applicationsOnly)
}

func runInstallCommand(ctx commandContext) {
	path, _ := ctx.Args.String("--path")
	installApp(ctx.Device, path)
}

func runUninstallCommand(ctx commandContext) {
	bundleID, _ := ctx.Args.String("<bundleID>")
	uninstallApp(ctx.Device, bundleID)
}

func runLangCommand(ctx commandContext) {
	locale, _ := ctx.Args.String("--setlocale")
	newlang, _ := ctx.Args.String("--setlang")
	slog.Debug("lang", "setlocale", locale, "setlang", newlang)
	language(ctx.Device, locale, newlang)
}

func runDproxyCommand(ctx commandContext) {
	binaryMode, _ := ctx.Args.Bool("--binary")
	startDebugProxy(ctx.Device, binaryMode)
}

func runInfoCommand(ctx commandContext) {
	if display, _ := ctx.Args.Bool("display"); display {
		deviceInfo, err := deviceinfo.NewDeviceInfo(ctx.Device)
		exitIfError("Can't connect to deviceinfo service", err)
		defer deviceInfo.Close()

		info, err := deviceInfo.GetDisplayInfo()
		exitIfError("Can't fetch dispaly info", err)

		fmt.Println(convertToJSONString(info))
		return
	}
	printDeviceInfo(ctx.Device)
}

func runSyslogCommand(ctx commandContext) {
	parse, _ := ctx.Args.Bool("--parse")
	runSyslog(ctx.Device, parse)
}

func runOSTraceCommand(ctx commandContext) {
	pidStr, _ := ctx.Args.String("--pid")
	processName, _ := ctx.Args.String("--process")
	levelStr, _ := ctx.Args.String("--level")
	subsystem, _ := ctx.Args.String("--subsystem")
	match, _ := ctx.Args.String("--match")
	exclude, _ := ctx.Args.String("--exclude")
	pid := -1
	if pidStr != "" {
		var err error
		pid, err = strconv.Atoi(pidStr)
		exitIfError("invalid --pid value", err)
	}
	levelFilter, err := ostrace.ParseLevelFilter(levelStr)
	exitIfError("invalid --level value", err)
	clientFilter := ostrace.ClientFilter{
		Levels:    levelFilter.ClientLevels,
		Subsystem: subsystem,
		Match:     match,
		Exclude:   exclude,
	}
	follow, _ := ctx.Args.Bool("--follow")
	runOsTrace(ctx.Device, pid, processName, levelFilter.MessageFilter, levelFilter.StreamFlags, clientFilter, follow)
}

func runScreenshotCommand(ctx commandContext) {
	stream, _ := ctx.Args.Bool("--stream")
	port, _ := ctx.Args.String("--port")
	path, _ := ctx.Args.String("--output")
	if stream {
		if port == "" {
			port = "3333"
		}
		err := instruments.StartMJPEGStreamingServer(ctx.Device, port)
		exitIfError("failed starting mjpeg", err)
		return
	}
	saveScreenshot(ctx.Device, path)
}

func runResetLocationCommand(ctx commandContext) {
	resetLocation(ctx.Device)
}

func runDeviceNameCommand(ctx commandContext) {
	printDeviceName(ctx.Device)
}

func runAppsCommand(ctx commandContext) {
	list, _ := ctx.Args.Bool("--list")
	system, _ := ctx.Args.Bool("--system")
	all, _ := ctx.Args.Bool("--all")
	filesharing, _ := ctx.Args.Bool("--filesharing")
	printInstalledApps(ctx.Device, system, all, list, filesharing)
}

func runDateCommand(ctx commandContext) {
	printDeviceDate(ctx.Device)
}

func runDiagnosticsCommand(ctx commandContext) {
	printDiagnostics(ctx.Device)
}

func runPairCommand(ctx commandContext) {
	org, _ := ctx.Args.String("--p12file")
	pwd, _ := ctx.Args.String("--password")
	if pwd == "" {
		pwd = os.Getenv("P12_PASSWORD")
	}
	pairDevice(ctx.Device, org, pwd)
}

func runReadPairCommand(ctx commandContext) {
	readPair(ctx.Device)
}

func runBatteryRegistryCommand(ctx commandContext) {
	printBatteryRegistry(ctx.Device)
}

func runRebootCommand(ctx commandContext) {
	err := diagnostics.Reboot(ctx.Device)
	if err != nil {
		slog.Error("reboot failed", "error", err)
	} else {
		slog.Info("ok")
	}
}

func runShutdownCommand(ctx commandContext) {
	err := diagnostics.Shutdown(ctx.Device)
	if err != nil {
		slog.Error("shutdown failed", "error", err)
	} else {
		slog.Info("ok")
	}
}

func runDiskspaceCommand(ctx commandContext) {
	afcService, err := afc.New(ctx.Device)
	exitIfError("connect afc service failed", err)
	info, err := afcService.DeviceInfo()
	exitIfError("get device info push failed", err)
	if JSONdisabled {
		fmt.Printf("      Model: %s\n", info.Model)
		fmt.Printf("  BlockSize: %d\n", info.BlockSize)
		fmt.Printf("  FreeSpace: %s\n", ios.ByteCountDecimal(int64(info.FreeBytes)))
		fmt.Printf("  UsedSpace: %s\n", ios.ByteCountDecimal(int64(info.TotalBytes-info.FreeBytes)))
		fmt.Printf(" TotalSpace: %s\n", ios.ByteCountDecimal(int64(info.TotalBytes)))
	} else {
		fmt.Println(convertToJSONString(info))
	}
}

func runBatteryCheckCommand(ctx commandContext) {
	printBatteryDiagnostics(ctx.Device)
}
