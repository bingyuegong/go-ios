package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bingyuegong/go-ios/ios/tunnel"
	"github.com/docopt/docopt-go"
)

type tunnelCommandContext struct {
	Args           docopt.Opts
	TunnelInfoHost string
	TunnelInfoPort int
}

func isTunnelCommand(args docopt.Opts) bool {
	return boolArg(args, "tunnel")
}

// tunnelTargetUDID returns the device udid for a per-device tunnel command,
// falling back to the GO_IOS_UDID environment variable.
func tunnelTargetUDID(args docopt.Opts) string {
	udid, _ := args.String("-u")
	if udid == "" {
		udid = os.Getenv("GO_IOS_UDID")
	}
	return udid
}

// tunnelPortForUDID 返回指定 udid 应使用的 tunnel-info-port。
// 若用户显式传了 --tunnel-info-port（即 ctx.TunnelInfoPort != 默认值），直接使用；
// 否则从本地注册表查询，查不到则降级到默认 60105。
func tunnelPortForUDID(ctx tunnelCommandContext, udid string) int {
	_, explicitErr := ctx.Args.Int("--tunnel-info-port")
	if explicitErr == nil {
		// 用户显式指定了端口，直接使用
		return ctx.TunnelInfoPort
	}
	if udid != "" {
		if port, ok := globalTunnelPortRegistry.Lookup(udid); ok {
			return port
		}
	}
	return ctx.TunnelInfoPort // 降级到默认 60105
}

func dispatchTunnelCommand(ctx tunnelCommandContext) bool {
	if !isTunnelCommand(ctx.Args) {
		return false
	}

	startCommand, _ := ctx.Args.Bool("start")
	stopCommand, _ := ctx.Args.Bool("stop")
	refreshCommand, _ := ctx.Args.Bool("refresh")
	useUserspaceNetworking, _ := ctx.Args.Bool("--userspace")
	if startCommand && !useUserspaceNetworking {
		err := tunnel.CheckPermissions()
		exitIfError("If --userspace is not set, we need sudo, an admin shell on Windows, or CAP_NET_ADMIN on Linux", err)
	}
	if useUserspaceNetworking {
		slog.Info("Using userspace networking")
	}
	stopagent, _ := ctx.Args.Bool("stopagent")
	listCommand, _ := ctx.Args.Bool("ls")
	if startCommand {
		pairRecordsPath, _ := ctx.Args.String("--pair-record-path")
		if len(pairRecordsPath) == 0 {
			pairRecordsPath = "."
		}
		if strings.ToLower(pairRecordsPath) == "default" {
			pairRecordsPath = "/var/db/lockdown/RemotePairing/user_501"
			slog.Warn("'--pair-record-path=default' reads Apple's own RemotePairing identity, "+
				"which macOS 26 (Tahoe) and newer block for third-party binaries via TCC "+
				"('operation not permitted'). If the tunnel fails to read selfIdentity.plist, "+
				"drop '=default' and pass a stable writable directory instead "+
				"(e.g. --pair-record-path=/Users/Shared/go-ios); go-ios then manages its own "+
				"tunnel identity and pairs it on first use. See https://github.com/bingyuegong/go-ios/issues/710",
				"pairRecordsPath", pairRecordsPath)
		}
		// If --udid is given, restrict this agent to that one device so it can run
		// as an isolated per-device tunnel agent (see NewTunnelManagerForDevice).
		startTunnel(context.TODO(), pairRecordsPath, ctx.TunnelInfoHost, ctx.TunnelInfoPort, useUserspaceNetworking, tunnelTargetUDID(ctx.Args))
	} else if listCommand {
		tunnels, err := tunnel.ListRunningTunnels(ctx.TunnelInfoHost, ctx.TunnelInfoPort)
		exitIfError("failed to get tunnel infos", err)
		if JSONdisabled {
			for index, t := range tunnels {
				if 0 != index {
					fmt.Println()
				}
				fmt.Printf("Udid: %s\n  Address: %s\n  RsdPort: %d\n  UserspaceTUN: %v\n  UserspaceTUNPort: %d\n",
					t.Udid, t.Address, t.RsdPort, t.UserspaceTUN, t.UserspaceTUNPort)
			}
		} else {
			fmt.Println(convertToJSONString(tunnels))
		}
	} else if stopCommand {
		udid := tunnelTargetUDID(ctx.Args)
		if udid == "" {
			exitIfError("failed to stop tunnel", fmt.Errorf("-u is required"))
		}
		port := tunnelPortForUDID(ctx, udid)
		err := tunnel.StopTunnelForDevice(udid, ctx.TunnelInfoHost, port)
		exitIfError("failed to stop tunnel", err)
		// 从本地注册表删除端口记录
		if regErr := globalTunnelPortRegistry.Unregister(udid); regErr != nil {
			slog.Warn("failed to unregister tunnel port", "udid", udid, "error", regErr)
		}
		if JSONdisabled {
			fmt.Printf("Stopped tunnel for %s\n", udid)
		} else {
			fmt.Println(convertToJSONString(map[string]string{"udid": udid, "status": "stopped"}))
		}
	} else if refreshCommand {
		udid := tunnelTargetUDID(ctx.Args)
		if udid == "" {
			exitIfError("failed to refresh tunnel", fmt.Errorf("-u is required"))
		}
		port := tunnelPortForUDID(ctx, udid)
		tun, err := tunnel.RefreshTunnelForDevice(udid, ctx.TunnelInfoHost, port, 30*time.Second)
		exitIfError("failed to refresh tunnel", err)
		if JSONdisabled {
			fmt.Printf("Refreshed tunnel for %s\n  Address: %s\n  RsdPort: %d\n  UserspaceTUN: %v\n  UserspaceTUNPort: %d\n",
				tun.Udid, tun.Address, tun.RsdPort, tun.UserspaceTUN, tun.UserspaceTUNPort)
		} else {
			fmt.Println(convertToJSONString(tun))
		}
	}
	if stopagent {
		err := tunnel.CloseAgent()
		if err != nil {
			exitIfError("failed to close agent", err)
		}
		// stopagent 停止所有 tunnel，清空本地端口注册表
		if regErr := globalTunnelPortRegistry.Clear(); regErr != nil {
			slog.Warn("failed to clear tunnel port registry", "error", regErr)
		}
	}
	return true
}
