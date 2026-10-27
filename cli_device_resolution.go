package main

import (
	"os"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/tunnel"
	"github.com/docopt/docopt-go"
)

type tunnelInfoConfig struct {
	Host string
	Port int
}

func tunnelInfoConfigFromArgs(arguments docopt.Opts) tunnelInfoConfig {
	tunnelInfoHost, err := arguments.String("--tunnel-info-host")
	if err != nil || tunnelInfoHost == "" {
		tunnelInfoHost = ios.HttpApiHost()
	}

	tunnelInfoPort, err := arguments.Int("--tunnel-info-port")
	// 用户未显式指定 --tunnel-info-port 时，尝试从注册表按 udid 查询
	if err != nil {
		udid, _ := arguments.String("-u")
		if udid == "" {
			udid = os.Getenv("GO_IOS_UDID")
		}
		if udid != "" {
			if registryPort, ok := globalTunnelPortRegistry.Lookup(udid); ok {
				tunnelInfoPort = registryPort
			} else {
				tunnelInfoPort = ios.HttpApiPort() // 降级到默认 60105
			}
		} else {
			tunnelInfoPort = ios.HttpApiPort() // 无 udid，使用默认 60105
		}
	}

	return tunnelInfoConfig{
		Host: tunnelInfoHost,
		Port: tunnelInfoPort,
	}
}

func resolveDevice(arguments docopt.Opts, tunnelInfo tunnelInfoConfig) ios.DeviceEntry {
	udid, _ := arguments.String("-u")
	if udid == "" {
		udid = os.Getenv("GO_IOS_UDID")
	}
	address, addressErr := arguments.String("--address")
	rsdPort, rsdErr := arguments.Int("--rsd-port")
	userspaceTunnelHost, userspaceTunnelHostErr := arguments.String("--userspace-host")
	if userspaceTunnelHostErr != nil {
		userspaceTunnelHost = ios.HttpApiHost()
	}

	userspaceTunnelPort, userspaceTunnelErr := arguments.Int("--userspace-port")

	lookupStarted := time.Now()
	diagnosticInfo("device lookup started", "requested_udid", udid)
	device, err := ios.GetDevice(udid)
	if err != nil {
		diagnosticError("device lookup failed", "requested_udid", udid, "duration_ms", time.Since(lookupStarted).Milliseconds(), "error", err)
	} else {
		diagnosticInfo("device lookup completed", "requested_udid", udid, "resolved_udid", device.Properties.SerialNumber, "duration_ms", time.Since(lookupStarted).Milliseconds())
	}
	if isTunnelCommand(arguments) {
		return device
	}

	exitIfError("Device not found: "+udid, err)
	if addressErr == nil && rsdErr == nil {
		diagnosticInfo("device RSD connection started", "udid", device.Properties.SerialNumber, "address", address, "port", rsdPort, "source", "explicit")
		if userspaceTunnelErr == nil {
			device.UserspaceTUN = true
			device.UserspaceTUNHost = userspaceTunnelHost
			device.UserspaceTUNPort = userspaceTunnelPort
		}
		return deviceWithRsdProvider(device, udid, address, rsdPort)
	}

	if !needsAutomaticTunnelInfo(arguments) {
		diagnosticInfo("tunnel info lookup skipped", "udid", device.Properties.SerialNumber, "reason", "command does not require automatic tunnel info")
		return device
	}

	// tunnelInfoConfigFromArgs 在解析参数时可能还不知道真实 udid（未传 -u），
	// 此处已拿到真实 udid，若注册表里有对应端口则优先使用。
	resolvedPort := tunnelInfo.Port
	if registryPort, ok := globalTunnelPortRegistry.Lookup(device.Properties.SerialNumber); ok {
		resolvedPort = registryPort
	}

	lookupStarted = time.Now()
	diagnosticInfo("tunnel info lookup started", "udid", device.Properties.SerialNumber, "host", tunnelInfo.Host, "port", resolvedPort)
	info, err := tunnel.TunnelInfoForDevice(device.Properties.SerialNumber, tunnelInfo.Host, resolvedPort)
	if err == nil {
		diagnosticInfo("tunnel info lookup completed", "udid", device.Properties.SerialNumber, "address", info.Address, "rsd_port", info.RsdPort, "duration_ms", time.Since(lookupStarted).Milliseconds())
		device.UserspaceTUNPort = info.UserspaceTUNPort
		device.UserspaceTUNHost = userspaceTunnelHost
		device.UserspaceTUN = info.UserspaceTUN
		return deviceWithRsdProvider(device, udid, info.Address, info.RsdPort)
	}

	diagnosticWarn("tunnel info lookup failed; falling back to usbmuxd", "udid", device.Properties.SerialNumber, "host", tunnelInfo.Host, "port", resolvedPort, "duration_ms", time.Since(lookupStarted).Milliseconds(), "error", err)
	return device
}

// Keyboard commands either manage a daemon or send keys to one that is
// already running; neither path needs to resolve or open a device here.
func isDeviceIndependentKeyboardCommand(args docopt.Opts) bool {
	return boolArg(args, "keyboard")
}

func needsAutomaticTunnelInfo(args docopt.Opts) bool {
	if boolArg(args, "rsd") || boolArg(args, "file") || boolArg(args, "webinspector") {
		return true
	}
	if boolArg(args, "info") && boolArg(args, "display") {
		return true
	}
	// `ui run` launches an XCUITest runner via testmanagerd, which needs the
	// tunnel on iOS 17+; the other `ui` commands are HTTP-to-a-URL and don't.
	if boolArg(args, "ui") && boolArg(args, "run") {
		return true
	}

	for _, commandName := range []string{
		"debug",
		"delete_all_testmanagerd_screenrecord",
		"devicestate",
		"instruments",
		"kill",
		"launch",
		"memlimitoff",
		"ostrace",
		"pasteboard",
		"ps",
		"pull_testmanagerd_screenrecord",
		"resetlocation",
		"runwda",
		"runxctest",
		"runtest",
		"screenshot",
		"setlocation",
		"setlocationgpx",
		"syslog",
		"sysmontap",
	} {
		if boolArg(args, commandName) {
			return true
		}
	}

	return false
}
