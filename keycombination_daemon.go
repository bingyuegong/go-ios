package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/docopt/docopt-go"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/hidkeyboard"
	"github.com/bingyuegong/go-ios/ios/tunnel"
)

// The daemon keeps one long-lived virtual keyboard session per device, so an
// attached virtual keyboard is available without recreating it per command.
// Other go-ios processes send key combinations over a loopback TCP socket.

type daemonRequest struct {
	Command string   `json:"command"`
	UDID    string   `json:"udid,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Token   string   `json:"token,omitempty"`
}

type daemonTargetResult struct {
	UDID  string `json:"udid"`
	Error string `json:"error,omitempty"`
}

type daemonResponse struct {
	OK      bool                 `json:"ok"`
	Message string               `json:"message,omitempty"`
	UDIDs   []string             `json:"udids,omitempty"`
	Pending []string             `json:"pending,omitempty"`
	Results []daemonTargetResult `json:"results,omitempty"`
	PID     int                  `json:"pid,omitempty"`
	Target  string               `json:"target,omitempty"`
}

type daemonState struct {
	Address string `json:"address"`
	PID     int    `json:"pid,omitempty"`
	UDID    string `json:"udid,omitempty"`
	Token   string `json:"token,omitempty"`
}

func daemonStatePath() string {
	if fromEnv := os.Getenv("GO_IOS_KEYBOARD_STATE"); fromEnv != "" {
		return fromEnv
	}
	return filepath.Join(os.TempDir(), "go-ios-keyboard-daemon.json")
}

func deviceDaemonStatePath(udid string) string {
	sum := sha256.Sum256([]byte(udid))
	base := daemonStatePath()
	return strings.TrimSuffix(base, filepath.Ext(base)) + "-" + hex.EncodeToString(sum[:12]) + ".json"
}

func keyboardDaemonUDID(args docopt.Opts) string {
	udid, _ := args.String("-u")
	if udid == "" {
		udid = os.Getenv("GO_IOS_UDID")
	}
	return udid
}

func runKeyboardDaemonCommand(ctx commandContext) {
	udid := keyboardDaemonUDID(ctx.Args)
	switch {
	case boolArg(ctx.Args, "start"):
		exitIfError("keyboard daemon start", startDeviceKeyboardDaemon(udid, ctx.Args))
	case boolArg(ctx.Args, "stop"):
		exitIfError("keyboard daemon stop", stopDeviceKeyboardDaemon(udid))
	default:
		runKeyCombinationDaemon(ctx)
	}
}

// runKeyCombinationDaemon polls for devices and serves key requests until
// interrupted. It never exits on a single device failure.
func runKeyCombinationDaemon(ctx commandContext) {
	target := keyboardDaemonUDID(ctx.Args)
	managed := os.Getenv("GO_IOS_KEYBOARD_BACKGROUND") == "1"
	if !managed {
		// The foreground daemon retains the original multi-device behavior.
		target = ""
	} else if target == "" || os.Getenv("GO_IOS_KEYBOARD_TOKEN") == "" {
		exitIfError("keyboard daemon", errors.New("managed daemon requires a target UDID and token"))
		return
	}
	interruptCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	runCtx, cancel := context.WithCancel(interruptCtx)
	defer cancel()

	tunnels := tunnelInfoConfigFromArgs(ctx.Args)
	listDevices := func() []ios.DeviceEntry {
		return eligibleKeyboardDevices(tunnels, target)
	}
	pool := newKeyboardPool(listDevices(), dialKeyboardSession)
	if managed && len(pool.UDIDs()) == 0 {
		slog.Warn("target device has no active virtual keyboard session; check tunnel, RSD service and daemon log", "udid", target, "tunnel_host", tunnels.Host, "tunnel_port", tunnels.Port)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	exitIfError("keyboard daemon: failed to listen", err)
	defer func() { _ = listener.Close() }()
	statePath := daemonStatePath()
	state := daemonState{Address: listener.Addr().String(), PID: os.Getpid()}
	if managed {
		statePath = deviceDaemonStatePath(target)
		state.UDID = target
		state.Token = os.Getenv("GO_IOS_KEYBOARD_TOKEN")
	}
	if err := writeDaemonStateRecord(statePath, state); err != nil {
		exitIfError("keyboard daemon: failed to write state file", err)
		return
	}
	defer func() {
		if err := pool.Close(); err != nil {
			slog.Warn("failed to close virtual keyboard sessions", "error", err)
		}
		_ = listener.Close()
		removeDaemonStateIfOwned(statePath, state)
	}()
	fmt.Printf("Virtual keyboard daemon listening on %s; state file %s\n", listener.Addr(), statePath)
	printKeyboardPoolStatus(pool)

	go serveKeyboardDaemonWithStop(listener, pool, target, state.Token, cancel)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			fmt.Println("\nStopping virtual keyboard daemon.")
			return
		case <-ticker.C:
			before := len(pool.UDIDs())
			pool.Sync(listDevices())
			if len(pool.UDIDs()) != before {
				printKeyboardPoolStatus(pool)
			}
		}
	}
}

// eligibleKeyboardDevices returns the connected devices that have a running
// tunnel. ios.ListDevices alone does not carry the RSD service table, so the
// tunnel info is fetched separately and attached per device.
func eligibleKeyboardDevices(tunnels tunnelInfoConfig, target string) []ios.DeviceEntry {
	host := tunnels.Host
	if host == "" {
		host = ios.HttpApiHost()
	}
	byUDID := make(map[string]ios.DeviceEntry)
	for _, port := range keyboardTunnelInfoPorts(tunnels) {
		devices, err := tunneledDevices(host, port, hidkeyboard.ServiceName, target)
		if err != nil {
			slog.Warn("failed to read tunnel info", "host", host, "port", port, "error", err)
			continue
		}
		for _, device := range devices {
			byUDID[device.Properties.SerialNumber] = device
		}
	}
	udids := make([]string, 0, len(byUDID))
	for udid := range byUDID {
		udids = append(udids, udid)
	}
	sort.Strings(udids)
	devices := make([]ios.DeviceEntry, 0, len(udids))
	for _, udid := range udids {
		devices = append(devices, byUDID[udid])
	}
	return devices
}

// keyboardTunnelInfoPorts lists the tunnel-info ports to query: every port in
// the local registry, plus the default port as a fallback.
func keyboardTunnelInfoPorts(tunnels tunnelInfoConfig) []int {
	ports := []int{}
	seen := make(map[int]bool)
	add := func(port int) {
		if port > 0 && !seen[port] {
			ports = append(ports, port)
			seen[port] = true
		}
	}
	add(tunnels.Port)
	for _, port := range globalTunnelPortRegistry.All() {
		add(port)
	}
	add(ios.HttpApiPort())
	return ports
}

// deviceWithRsd attaches an RSD provider to a device. Unlike the CLI helper
// with the same name, it returns an error instead of exiting, because a single
// device must not stop the daemon for the other devices.
func deviceWithRsd(device ios.DeviceEntry, udid string, address string, rsdPort int) (ios.DeviceEntry, error) {
	rsdService, err := ios.NewWithAddrPortDevice(address, rsdPort, device)
	if err != nil {
		return device, err
	}
	defer rsdService.Close()
	provider, err := rsdService.Handshake()
	if err != nil {
		return device, err
	}
	enriched, err := ios.GetDeviceWithAddress(udid, address, provider)
	if err != nil {
		return device, err
	}
	enriched.UserspaceTUN = device.UserspaceTUN
	enriched.UserspaceTUNHost = device.UserspaceTUNHost
	enriched.UserspaceTUNPort = device.UserspaceTUNPort
	return enriched, nil
}

func tunneledDevices(host string, port int, service string, target string) ([]ios.DeviceEntry, error) {
	tunnels, err := tunnel.ListRunningTunnels(host, port)
	if err != nil {
		return nil, err
	}
	if len(tunnels) == 0 {
		return nil, nil
	}
	list, err := ios.ListDevices()
	if err != nil {
		return nil, err
	}
	byUDID := make(map[string]tunnel.Tunnel, len(tunnels))
	for _, active := range tunnels {
		byUDID[active.Udid] = active
	}
	devices := make([]ios.DeviceEntry, 0, len(list.DeviceList))
	for _, device := range list.DeviceList {
		if target != "" && device.Properties.SerialNumber != target {
			continue
		}
		active, ok := byUDID[device.Properties.SerialNumber]
		if !ok {
			continue
		}
		device.UserspaceTUN = active.UserspaceTUN
		device.UserspaceTUNPort = active.UserspaceTUNPort
		device.UserspaceTUNHost = host
		enriched, err := deviceWithRsd(device, device.Properties.SerialNumber, active.Address, active.RsdPort)
		if err != nil {
			slog.Warn("failed to attach RSD provider", "udid", device.Properties.SerialNumber, "error", err)
			continue
		}
		if !enriched.SupportsRsd() || enriched.Rsd.GetPort(service) == 0 {
			slog.Warn("device does not expose virtual keyboard service", "udid", device.Properties.SerialNumber, "service", service)
			continue
		}
		devices = append(devices, enriched)
	}
	return devices, nil
}

func printKeyboardPoolStatus(pool *keyboardPool) {
	active := pool.UDIDs()
	pending := pool.Pending()
	fmt.Printf("Active virtual keyboard sessions: %d %v; pending: %v\n", len(active), active, pending)
}

func serveKeyboardDaemon(listener net.Listener, pool *keyboardPool) {
	serveKeyboardDaemonWithStop(listener, pool, "", "", nil)
}

func serveKeyboardDaemonWithStop(listener net.Listener, pool *keyboardPool, target, token string, stop func()) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			decoder := json.NewDecoder(conn)
			encoder := json.NewEncoder(conn)
			for {
				var request daemonRequest
				if err := decoder.Decode(&request); err != nil {
					return
				}
				response := daemonResponse{}
				if request.Command != "identity" && request.Command != "stop" && request.Command != "force-stop" {
					response = handleKeyboardDaemonRequest(pool, request)
				}
				if request.Command == "identity" {
					response = daemonResponse{OK: true, PID: os.Getpid(), Target: target}
				}
				if request.Command == "status" {
					response.PID = os.Getpid()
					response.Target = target
				}
				if request.Command == "stop" || request.Command == "force-stop" {
					response = daemonResponse{Message: "stop is unavailable for this daemon"}
					if stop != nil && token != "" && request.Token == token && request.UDID == target {
						response = daemonResponse{OK: true, PID: os.Getpid(), Target: target}
					}
				}
				if err := encoder.Encode(response); err != nil {
					return
				}
				if response.OK && request.Command == "stop" {
					stop()
					return
				}
				if response.OK && request.Command == "force-stop" {
					// The daemon exits itself: the client never kills a potentially
					// recycled PID read from a state file.
					os.Exit(0)
				}
			}
		}()
	}
}

func handleKeyboardDaemonRequest(pool *keyboardPool, request daemonRequest) daemonResponse {
	switch strings.ToLower(strings.TrimSpace(request.Command)) {
	case "keys":
		usages, err := parseKeyCombination(request.Keys)
		if err != nil {
			return daemonResponse{Message: err.Error()}
		}
		results := pool.PressKeys(request.UDID, usages)
		response := daemonResponse{OK: len(results) > 0, Results: results}
		if len(results) == 0 {
			response.Message = "no active virtual keyboard sessions"
		}
		for _, result := range results {
			if result.Error != "" {
				response.OK = false
			}
		}
		return response
	case "status":
		return daemonResponse{OK: true, UDIDs: pool.UDIDs(), Pending: pool.Pending()}
	default:
		return daemonResponse{Message: "unsupported daemon command " + request.Command}
	}
}

func writeDaemonState(path string, address string) error {
	return writeDaemonStateRecord(path, daemonState{Address: address})
}

func writeDaemonStateRecord(path string, state daemonState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write(data)
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func readDaemonState(path string) (daemonState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return daemonState{}, err
	}
	var state daemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("invalid daemon state file %s: %w", path, err)
	}
	return state, nil
}

func removeDaemonStateIfOwned(path string, expected daemonState) {
	current, err := readDaemonState(path)
	if err != nil || current != expected {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove daemon state file", "path", path, "error", err)
	}
}

func requestKeyboardDaemon(state daemonState, request daemonRequest) (daemonResponse, error) {
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return daemonResponse{}, fmt.Errorf("invalid loopback daemon address %q", state.Address)
	}
	conn, err := net.DialTimeout("tcp", state.Address, 2*time.Second)
	if err != nil {
		return daemonResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return daemonResponse{}, err
	}
	var response daemonResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return daemonResponse{}, err
	}
	return response, nil
}

// sendKeysToDaemon sends only through the already running, verified daemon
// belonging to the requested device. It never creates a temporary keyboard or
// falls back to a different (potentially multi-device) daemon.
func sendKeysToDaemon(udid string, keys []string) (daemonResponse, error) {
	if err := checkKeyboardDaemonUDID(udid); err != nil {
		return daemonResponse{}, err
	}
	path := deviceDaemonStatePath(udid)
	state, err := readDaemonState(path)
	if err != nil {
		if os.IsNotExist(err) {
			return daemonResponse{}, fmt.Errorf("no keyboard daemon running for %s; start it with ios keyboard daemon start -u %s", udid, udid)
		}
		return daemonResponse{}, err
	}
	status, err := deviceDaemonStatus(state, udid)
	if err != nil {
		return daemonResponse{}, fmt.Errorf("keyboard daemon for %s cannot be verified: %w", udid, err)
	}
	if !containsUDID(status.UDIDs, udid) {
		return daemonResponse{}, fmt.Errorf("keyboard daemon for %s has no active keyboard session", udid)
	}
	response, err := requestKeyboardDaemon(state, daemonRequest{Command: "keys", UDID: udid, Keys: keys})
	if err != nil {
		return response, fmt.Errorf("cannot reach keyboard daemon for %s: %w", udid, err)
	}
	if !response.OK {
		if response.Message != "" {
			return response, errors.New(response.Message)
		}
		for _, result := range response.Results {
			if result.Error != "" {
				return response, fmt.Errorf("device %s: %s", result.UDID, result.Error)
			}
		}
		return response, errors.New("daemon reported a failed key press")
	}
	return response, nil
}

func withDeviceDaemonLock(path string, action func() error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	unlock, err := lockDeviceDaemonFile(lock)
	if err != nil {
		return err
	}
	defer unlock()
	return action()
}

func checkKeyboardDaemonUDID(udid string) error {
	if udid == "" || strings.Trim(udid, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		return errors.New("specify a valid device UDID with -u <udid> or GO_IOS_UDID")
	}
	return nil
}

func deviceDaemonStatus(state daemonState, udid string) (daemonResponse, error) {
	if state.UDID != udid || state.PID <= 0 || state.Token == "" {
		return daemonResponse{}, errors.New("state file does not identify a managed daemon for this device")
	}
	response, err := requestKeyboardDaemon(state, daemonRequest{Command: "status"})
	if err != nil {
		return response, err
	}
	if !response.OK || response.PID != state.PID || response.Target != udid {
		return response, errors.New("daemon identity does not match the state file")
	}
	return response, nil
}

func containsUDID(udids []string, target string) bool {
	for _, udid := range udids {
		if udid == target {
			return true
		}
	}
	return false
}

func startDeviceKeyboardDaemon(udid string, args docopt.Opts) error {
	if err := checkKeyboardDaemonUDID(udid); err != nil {
		return err
	}
	path := deviceDaemonStatePath(udid)
	return withDeviceDaemonLock(path, func() error {
		state, err := readDaemonState(path)
		if err == nil {
			response, probeErr := deviceDaemonStatus(state, udid)
			if probeErr == nil {
				if containsUDID(response.UDIDs, udid) {
					fmt.Printf("Keyboard daemon already running for %s (PID %d).\n", udid, state.PID)
					return nil
				}
				return fmt.Errorf("keyboard daemon for %s is running but its session is not ready; refusing a duplicate start", udid)
			}
			if state.PID > 0 && processExists(state.PID) {
				return fmt.Errorf("keyboard daemon state %s is unreachable but PID %d exists; refusing a duplicate: %w", path, state.PID, probeErr)
			}
			removeDaemonStateIfOwned(path, state)
		} else if !os.IsNotExist(err) {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			return err
		}
		token := hex.EncodeToString(tokenBytes)
		argv := []string{"keyboard", "daemon", "-u", udid}
		if raw, ok := args["--tunnel-info-port"]; ok && raw != nil && raw != false && raw != "" {
			port, err := args.Int("--tunnel-info-port")
			if err != nil || port <= 0 || port > 65535 {
				return fmt.Errorf("invalid --tunnel-info-port %v", raw)
			}
			argv = append(argv, "--tunnel-info-port", fmt.Sprint(port))
		}
		if host, err := args.String("--tunnel-info-host"); err == nil && host != "" {
			argv = append(argv, "--tunnel-info-host", host)
		}
		logFile, err := os.OpenFile(path+".log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		cmd := exec.Command(executable, argv...)
		cmd.Env = append(os.Environ(), "GO_IOS_KEYBOARD_BACKGROUND=1", "GO_IOS_KEYBOARD_TOKEN="+token)
		cmd.Stdin = nil
		cmd.SysProcAttr = keyboardDaemonSysProcAttr()
		cmd.Stdout, cmd.Stderr = logFile, logFile
		err = cmd.Start()
		_ = logFile.Close()
		if err != nil {
			return err
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			state, readErr := readDaemonState(path)
			if readErr == nil && state.PID == cmd.Process.Pid && state.UDID == udid && state.Token == token {
				response, probeErr := deviceDaemonStatus(state, udid)
				if probeErr == nil && containsUDID(response.UDIDs, udid) {
					_ = cmd.Process.Release()
					fmt.Printf("Keyboard daemon started for %s (PID %d).\n", udid, state.PID)
					return nil
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		_ = cmd.Process.Kill() // We own this child process, not a PID read from a state file.
		_ = cmd.Wait()
		state, err = readDaemonState(path)
		if err == nil && state.PID == cmd.Process.Pid && state.UDID == udid && state.Token == token {
			removeDaemonStateIfOwned(path, state)
		}
		return fmt.Errorf("device %s did not establish a keyboard session within 20 seconds; see %s.log", udid, path)
	})
}

func stopDeviceKeyboardDaemon(udid string) error {
	if err := checkKeyboardDaemonUDID(udid); err != nil {
		return err
	}
	path := deviceDaemonStatePath(udid)
	return withDeviceDaemonLock(path, func() error {
		state, err := readDaemonState(path)
		if os.IsNotExist(err) {
			fmt.Printf("No keyboard daemon running for %s.\n", udid)
			return nil
		}
		if err != nil {
			return err
		}
		_, err = deviceDaemonStatus(state, udid)
		if err != nil {
			if !processExists(state.PID) {
				removeDaemonStateIfOwned(path, state)
				fmt.Printf("No keyboard daemon running for %s; stale state cleared.\n", udid)
				return nil
			}
			return fmt.Errorf("cannot verify keyboard daemon PID %d; refusing to kill an unverified process: %w", state.PID, err)
		}
		response, err := requestKeyboardDaemon(state, daemonRequest{Command: "stop", UDID: udid, Token: state.Token})
		if err != nil || !response.OK {
			return fmt.Errorf("stop request failed for %s: response=%+v, error=%v", udid, response, err)
		}
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			if current, readErr := readDaemonState(path); os.IsNotExist(readErr) || (readErr == nil && current != state) {
				fmt.Printf("Keyboard daemon stopped for %s.\n", udid)
				return nil
			}
			if !processExists(state.PID) {
				removeDaemonStateIfOwned(path, state)
				fmt.Printf("Keyboard daemon stopped for %s.\n", udid)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Ask the authenticated daemon to exit itself instead of sending
		// SIGKILL to a PID that could have been recycled by the OS.
		identity, err := requestKeyboardDaemon(state, daemonRequest{Command: "identity"})
		if err != nil || !identity.OK || identity.PID != state.PID || identity.Target != udid {
			return fmt.Errorf("daemon did not exit and can no longer be verified; refusing to force-stop PID %d: response=%+v, error=%v", state.PID, identity, err)
		}
		response, err = requestKeyboardDaemon(state, daemonRequest{Command: "force-stop", UDID: udid, Token: state.Token})
		if err != nil || !response.OK {
			return fmt.Errorf("force-stop request failed for %s: response=%+v, error=%v", udid, response, err)
		}
		removeDaemonStateIfOwned(path, state)
		fmt.Printf("Keyboard daemon force-stopped for %s.\n", udid)
		return nil
	})
}
