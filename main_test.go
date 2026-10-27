package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	update = flag.Bool("update", false, "update golden files")
	e2e    = flag.Bool("e2e", false, "test with realdevice")
)

func TestDeviceList(t *testing.T) {
	if !*e2e {
		return
	}
	output, err := exec.Command("go", "run", "./ios.go", "list").Output()
	if err != nil {
		fmt.Println(err.Error())
	}
	fmt.Println(string(output))
}

func TestHelp_GlobalNoArgs(t *testing.T) {
	stdout, stderr, code := runCLI(t)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertGolden(t, "global.golden", stdout)
}

func TestHelp_GlobalFlagsEquivalent(t *testing.T) {
	stdoutLong, stderrLong, codeLong := runCLI(t, "--help")
	if codeLong != 0 || stderrLong != "" {
		t.Fatalf("--help failed: code=%d stderr=%q", codeLong, stderrLong)
	}
	stdoutShort, stderrShort, codeShort := runCLI(t, "-h")
	if codeShort != 0 || stderrShort != "" {
		t.Fatalf("-h failed: code=%d stderr=%q", codeShort, stderrShort)
	}
	if eolFormat(stdoutLong) != eolFormat(stdoutShort) {
		t.Fatalf("help outputs differ\n--help:\n%s\n-h:\n%s", stdoutLong, stdoutShort)
	}
}

func TestHelp_SubcommandExplicitImplicitAndGlobalFlagOrdering(t *testing.T) {
	explicit, explicitErr, explicitCode := runCLI(t, "help", "apps")
	if explicitCode != 0 || explicitErr != "" {
		t.Fatalf("help apps failed: code=%d stderr=%q", explicitCode, explicitErr)
	}
	implicit, implicitErr, implicitCode := runCLI(t, "apps", "--help")
	if implicitCode != 0 || implicitErr != "" {
		t.Fatalf("apps --help failed: code=%d stderr=%q", implicitCode, implicitErr)
	}
	implicitShort, implicitShortErr, implicitShortCode := runCLI(t, "apps", "-h")
	if implicitShortCode != 0 || implicitShortErr != "" {
		t.Fatalf("apps -h failed: code=%d stderr=%q", implicitShortCode, implicitShortErr)
	}
	withGlobal, withGlobalErr, withGlobalCode := runCLI(t, "-u", "abc", "help", "apps")
	if withGlobalCode != 0 || withGlobalErr != "" {
		t.Fatalf("-u abc help apps failed: code=%d stderr=%q", withGlobalCode, withGlobalErr)
	}
	normalizedExplicit := eolFormat(explicit)
	if normalizedExplicit != eolFormat(implicit) {
		t.Fatalf("explicit and implicit help differ")
	}
	if normalizedExplicit != eolFormat(implicitShort) {
		t.Fatalf("explicit and short implicit help differ")
	}
	if normalizedExplicit != eolFormat(withGlobal) {
		t.Fatalf("explicit and global-flag help differ")
	}
	assertGolden(t, "apps.golden", explicit)
}

func TestHelp_Pasteboard(t *testing.T) {
	explicit, stderr, code := runCLI(t, "help", "pasteboard")
	if code != 0 || stderr != "" {
		t.Fatalf("help pasteboard failed: code=%d stderr=%q", code, stderr)
	}
	implicit, stderr, code := runCLI(t, "pasteboard", "--help")
	if code != 0 || stderr != "" || eolFormat(implicit) != eolFormat(explicit) {
		t.Fatalf("pasteboard --help differs: code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(explicit, "ios pasteboard (set [<text>] | get) [options]") {
		t.Fatalf("missing pasteboard usage: %q", explicit)
	}
}

func TestHelp_Keyboard(t *testing.T) {
	explicit, stderr, code := runCLI(t, "help", "keyboard")
	if code != 0 || stderr != "" {
		t.Fatalf("help keyboard failed: code=%d stderr=%q", code, stderr)
	}
	implicit, stderr, code := runCLI(t, "keyboard", "--help")
	if code != 0 || stderr != "" || eolFormat(implicit) != eolFormat(explicit) {
		t.Fatalf("keyboard --help differs: code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(explicit, "ios keyboard <key>... [options]") || !strings.Contains(explicit, "daemon") || strings.Contains(explicit, "press <key>") || strings.Contains(explicit, "ios keyboard (session") {
		t.Fatalf("missing keyboard usage: %q", explicit)
	}
}

func TestKeyboardKeysRequireManagedDaemon(t *testing.T) {
	t.Setenv("GO_IOS_KEYBOARD_STATE", filepath.Join(t.TempDir(), "missing-daemon.json"))
	t.Setenv("GO_IOS_UDID", "")
	const udid = "00008110-000E2C8C3AF1401E"
	for _, args := range [][]string{
		{"keyboard", "a", "-u", udid},
		{"keyboard", "7", "-u", udid},
		{"keyboard", "command", "a", "-u", udid},
	} {
		stdout, stderr, code := runCLI(t, args...)
		if code == 0 || !strings.Contains(stderr, "daemon start -u "+udid) || strings.Contains(stdout, "HID reports sent") {
			t.Fatalf("keyboard %v without daemon: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{
		{"keyboard", "a"},
		{"keyboard", "press", "a", "-u", udid},
		{"keyboard", "session", "-u", udid},
	} {
		stdout, stderr, code := runCLI(t, args...)
		if code == 0 || strings.Contains(stdout, "HID reports sent") || strings.Contains(stderr, "Device not found") {
			t.Fatalf("unsupported keyboard %v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestHelp_NestedSubcommandAndUnknown(t *testing.T) {
	explicit, explicitErr, explicitCode := runCLI(t, "help", "tunnel", "start")
	if explicitCode != 0 || explicitErr != "" {
		t.Fatalf("help tunnel start failed: code=%d stderr=%q", explicitCode, explicitErr)
	}
	implicit, implicitErr, implicitCode := runCLI(t, "tunnel", "start", "--help")
	if implicitCode != 0 || implicitErr != "" {
		t.Fatalf("tunnel start --help failed: code=%d stderr=%q", implicitCode, implicitErr)
	}
	if eolFormat(explicit) != eolFormat(implicit) {
		t.Fatalf("nested explicit and implicit help differ")
	}

	stdout, stderr, code := runCLI(t, "help", "nope")
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown topic")
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty for unknown topic, got %q", stdout)
	}
	if !strings.Contains(stderr, "unknown help topic") {
		t.Fatalf("missing unknown topic message: %q", stderr)
	}
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Dir = "."
	logDir := t.TempDir()
	if err := os.Chmod(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "GO_IOS_LOG_DIR="+logDir)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run cli: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func assertGolden(t *testing.T, goldenName string, got string) {
	t.Helper()
	path := filepath.Join("testdata", "help", goldenName)
	got = eolFormat(got)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", goldenName, err)
	}
	want := eolFormat(string(wantBytes))
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", goldenName, got, want)
	}
}

func eolFormat(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func TestExecutionLogCoversHelpAndFailures(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ios")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cli: %v: %s", err, output)
	}
	logDir := t.TempDir()
	if err := os.Chmod(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (string, string, int) {
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "GO_IOS_LOG_DIR="+logDir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err == nil {
			return stdout.String(), stderr.String(), 0
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("execute cli: %v", err)
		}
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	stdout, stderr, code := invoke("--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "go-ios") {
		t.Fatalf("help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = invoke("version")
	if code != 0 || stderr != "" || stdout == "" {
		t.Fatalf("version console changed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, stderr, code = invoke("help", "nope")
	if code == 0 || !strings.Contains(stderr, "unknown help topic") {
		t.Fatalf("bad help: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code = invoke("-u", "nonexistent-device-for-log-test", "screenshot", "-output", "test.png")
	if code == 0 || !strings.Contains(stderr, "Device not found") || strings.Contains(stderr, "device lookup started") {
		t.Fatalf("screenshot console changed: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code = invoke("-u", "nonexistent-device-for-log-test", "info", "--verbose")
	if code == 0 || !strings.Contains(stderr, "Device not found") || strings.Contains(stderr, "device lookup started") || strings.Contains(stderr, "command parsing started") {
		t.Fatalf("non-screenshot console changed: code=%d stderr=%q", code, stderr)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var starts, finishes int
	var loggedStdout, loggedStderr, loggedDiagnostics string
	var screenshotArgs []string
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(logDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(entry.Name(), "-events-"):
			for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
				var event struct {
					Event    string   `json:"event"`
					Args     []string `json:"args"`
					ExitCode *int     `json:"exit_code"`
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Event == "start" {
					starts++
					if slices.Contains(event.Args, "screenshot") {
						screenshotArgs = event.Args
					}
				} else if event.Event == "finish" {
					finishes++
					if event.ExitCode == nil {
						t.Fatal("finish event missing exit code")
					}
				}
			}
		case strings.Contains(entry.Name(), "-stdout-"):
			loggedStdout += string(content)
		case strings.Contains(entry.Name(), "-stderr-"):
			loggedStderr += string(content)
		case strings.Contains(entry.Name(), "-diagnostic-"):
			loggedDiagnostics += string(content)
		}
	}
	if starts != 5 || finishes != 5 || !strings.Contains(loggedStdout, "go-ios") || !strings.Contains(loggedStderr, "unknown help topic") || !strings.Contains(loggedStderr, "Device not found") || !slices.Contains(screenshotArgs, "nonexistent-device-for-log-test") {
		t.Fatalf("logs: starts=%d finishes=%d args=%q stdout=%q stderr=%q", starts, finishes, screenshotArgs, loggedStdout, loggedStderr)
	}
	for _, phase := range []string{"command parsing started", "command parsing completed", "command dispatch started", "command dispatch completed", "device lookup started", "device lookup failed", "nonexistent-device-for-log-test"} {
		if !strings.Contains(loggedDiagnostics, phase) {
			t.Fatalf("diagnostic log missing %q: %s", phase, loggedDiagnostics)
		}
	}
	if !strings.Contains(loggedDiagnostics, "cli_device_resolution.go") || !strings.Contains(loggedDiagnostics, "\"run_id\"") {
		t.Fatalf("diagnostic log missing source or run ID: %s", loggedDiagnostics)
	}
	if strings.Contains(loggedStderr, "device lookup started") || strings.Contains(loggedStderr, "command parsing started") {
		t.Fatalf("diagnostic stages leaked into console stderr: %s", loggedStderr)
	}
}
