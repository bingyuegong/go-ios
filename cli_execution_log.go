package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bingyuegong/go-ios/internal/clihelp"
	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/golog"
	"github.com/docopt/docopt-go"
)

const (
	executionLogChildEnv    = "GO_IOS_EXECUTION_LOG_CHILD"
	executionLogDetachedEnv = "GO_IOS_EXECUTION_LOG_DETACHED"
	executionLogDeadlineEnv = "GO_IOS_EXECUTION_LOG_DEADLINE"
	// 同一设备的 screenshot 串行排队；排队等待与执行共用这一个整体预算。
	executionScreenshotLockEnv = "GO_IOS_EXECUTION_LOG_SCREENSHOT_LOCK"
	executionScreenshotBudget  = 20 * time.Second
	// 总预算内预留 100ms 回收 child、记录结果和关闭日志，不额外延长预算。
	executionScreenshotReap = 100 * time.Millisecond
	executionLogDirEnv      = "GO_IOS_LOG_DIR"
	executionLogMaxSize     = 16 << 20 // Rotate large streams as well as at the UTC day boundary.
	executionLogLifetime    = 72 * time.Hour
)

var executionLogName = regexp.MustCompile(`^go-ios-\d{8}-\d+-[0-9a-f]{16}-(stdout|stderr|events|diagnostic)-\d{6}\.log$`)

const executionDiagnosticDirEnv = "GO_IOS_EXECUTION_DIAGNOSTIC_DIR"
const executionDiagnosticRunIDEnv = "GO_IOS_EXECUTION_DIAGNOSTIC_RUN_ID"

var executionDiagnosticWriter *executionLogWriter
var executionDiagnosticRunID string

// diagnosticInfo is reserved for file-only stage records; normal slog calls
// still follow the user's existing console level and format.
func diagnosticInfo(msg string, args ...any)  { diagnosticStage(slog.LevelInfo, msg, args...) }
func diagnosticWarn(msg string, args ...any)  { diagnosticStage(slog.LevelWarn, msg, args...) }
func diagnosticError(msg string, args ...any) { diagnosticStage(slog.LevelError, msg, args...) }

func diagnosticStage(level slog.Level, msg string, args ...any) {
	logger := slog.Default()
	ctx := context.Background()
	if !logger.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	// runtime.Callers, diagnosticStage, diagnosticInfo/Warn/Error, caller.
	runtime.Callers(3, pcs[:])
	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.Add(args...)
	record.Add(golog.DiagnosticOnlyAttr, true)
	_ = logger.Handler().Handle(ctx, record)
}

// The diagnostic stream receives all regular CLI/library logs, including
// Debug/Trace, plus diagnostic-only stage records. The console never sees the
// latter, even when --verbose or --trace is supplied.
type executionDiagnosticHandler struct {
	console slog.Handler
	file    slog.Handler
}

func (h *executionDiagnosticHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.console.Enabled(ctx, level) || (h.file != nil && h.file.Enabled(ctx, level))
}

func (h *executionDiagnosticHandler) Handle(ctx context.Context, record slog.Record) error {
	diagnosticOnly := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == golog.DiagnosticOnlyAttr && attr.Value.Kind() == slog.KindBool && attr.Value.Bool() {
			diagnosticOnly = true
		}
		return true
	})
	var fileErr error
	if h.file != nil && h.file.Enabled(ctx, record.Level) {
		fileErr = h.file.Handle(ctx, record)
	}
	if diagnosticOnly || !h.console.Enabled(ctx, record.Level) {
		return fileErr
	}
	return errors.Join(fileErr, h.console.Handle(ctx, record))
}

func (h *executionDiagnosticHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := &executionDiagnosticHandler{console: h.console.WithAttrs(attrs)}
	if h.file != nil {
		clone.file = h.file.WithAttrs(attrs)
	}
	return clone
}

func (h *executionDiagnosticHandler) WithGroup(name string) slog.Handler {
	clone := &executionDiagnosticHandler{console: h.console.WithGroup(name)}
	if h.file != nil {
		clone.file = h.file.WithGroup(name)
	}
	return clone
}

// WriteRecord keeps each JSON log record intact across size rotation.
type executionDiagnosticSink struct{ writer *executionLogWriter }

func (s executionDiagnosticSink) Write(p []byte) (int, error) { return s.writer.WriteRecord(p) }

func newExecutionCLIHandler(console slog.Handler) slog.Handler {
	var file slog.Handler
	if executionDiagnosticWriter != nil {
		file = slog.NewJSONHandler(executionDiagnosticSink{executionDiagnosticWriter}, &slog.HandlerOptions{
			Level: ios.LevelTrace, AddSource: true, ReplaceAttr: replaceTraceLevel,
		}).WithAttrs([]slog.Attr{slog.String("run_id", executionDiagnosticRunID)})
	}
	return &executionDiagnosticHandler{console: console, file: file}
}

func replaceTraceLevel(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.LevelKey && a.Value.Kind() == slog.KindAny && a.Value.Any() == ios.LevelTrace {
		a.Value = slog.StringValue("TRACE")
	}
	return a
}

func defaultExecutionLogDir(goos, tempDir string, uid int) string {
	if goos == "windows" {
		// The Windows temp folder is user-specific; os.Getuid returns -1 there.
		return filepath.Join(tempDir, "go-ios-logs")
	}
	return filepath.Join("/var/tmp", fmt.Sprintf("go-ios-logs-%d", uid))
}

func validExecutionLogDir(info os.FileInfo, goos string) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && (goos == "windows" || info.Mode().Perm()&0o077 == 0)
}

type executionLogEvent struct {
	Time       time.Time `json:"time"`
	RunID      string    `json:"run_id"`
	PID        int       `json:"pid,omitempty"`
	Event      string    `json:"event"`
	Args       []string  `json:"args,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// 与 Main 共享参数规范化顺序和 Options 表；分类不写帮助、不退出、不配置设备。
func executionLogCommand(args []string) string {
	command, _ := executionLogInvocation(args)
	return command
}

// 同时返回目标设备：截图串行队列以 UDID 为键。未指定 -u 时使用 GO_IOS_UDID，
// 两者都没有则使用固定键 "default"（Main 此时会自行选择设备）。
func executionLogInvocation(args []string) (command, udid string) {
	// 命令词必须作为独立参数出现，缺少这两个词时一定不是受管命令。
	if !slices.Contains(args, "screenshot") && !slices.Contains(args, "daemon") {
		return "", ""
	}
	return executionLogInvocationWithUsage(args, executionLogUsage())
}

// 完整文法解析约需 290ms，会白白消耗 5 秒预算：耗时来自每条命令行里被展开成
// 近百个选项的 [options]。分类只需要 screenshot 与 keyboard 命令行完整展开；
// 其它命令行保留（去掉 [options]），因为 docopt 展开 [options] 时要排除“其它
// 命令行显式写出的选项”，直接删除会让 --log-output 之类被误当作 screenshot 的选项。
func executionLogUsage() string {
	lines := strings.Split(cliUsage(), "\n")
	kept := lines[:0:0]
	inUsage := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Usage:":
			inUsage = true
		case inUsage && trimmed == "":
			inUsage = false
		case inUsage && !strings.HasPrefix(trimmed, "ios screenshot") && !strings.HasPrefix(trimmed, "ios ui screenshot") && !strings.HasPrefix(trimmed, "ios keyboard"):
			line = strings.ReplaceAll(line, "[options]", "")
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func executionLogInvocationWithUsage(args []string, usage string) (command, udid string) {
	catalog, err := clihelp.Load()
	if err != nil {
		return "", ""
	}
	if _, handled := catalog.ResolveHelp(args); handled {
		return "", ""
	}
	parser := docopt.Parser{HelpHandler: func(error, string) {}}
	opts, err := parser.ParseArgs(normalizeUsageFlags(usage), normalizeShortFlags(args), "")
	if err != nil {
		return "", ""
	}
	// ui screenshot 也会设置 screenshot，必须依据解析后的命令标记排除。
	if boolArg(opts, "screenshot") && !boolArg(opts, "ui") && !boolArg(opts, "--stream") {
		udid, _ = opts.String("-u")
		if udid == "" {
			udid = os.Getenv("GO_IOS_UDID")
		}
		if udid == "" {
			udid = "default"
		}
		return "screenshot", udid
	}
	// keyboard <key>... 中的 "daemon" 只是参数值，不能据原始词判断后台模式。
	if boolArg(opts, "keyboard") && boolArg(opts, "daemon") && !boolArg(opts, "start") && !boolArg(opts, "stop") {
		return "keyboard daemon", ""
	}
	return "", ""
}

// 所有阻塞 I/O（包括 Wait 的复制协程和日志 Close）之外的独立进程截止器。
// 只保存本次 cmd.Start 返回的 Process，绝不按数字 PID 查找/杀进程。
// os.Process 的 Kill/Wait 同步负责拒绝已回收进程，避免 PID 复用误杀。
type executionDeadline struct {
	mu         sync.Mutex
	stopped    bool
	timedOut   bool
	process    *os.Process
	onTimeout  func()
	cutoff     time.Time
	soft, hard *time.Timer
}

func newExecutionDeadline(deadline time.Time, reap time.Duration, exit func(int)) *executionDeadline {
	d := &executionDeadline{cutoff: deadline.Add(-reap)}
	if reap > 0 {
		d.soft = time.AfterFunc(time.Until(deadline.Add(-reap)), func() {
			d.mu.Lock()
			if d.stopped {
				d.mu.Unlock()
				return
			}
			d.timedOut = true
			process, notify := d.process, d.onTimeout
			d.mu.Unlock()
			if process != nil {
				go process.Kill()
			}
			if notify != nil {
				go notify() // 超时日志只尽力写入，不能成为退出的前置条件。
			}
		})
	}
	d.hard = time.AfterFunc(time.Until(deadline), func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.stopped {
			d.timedOut = true
			// 不先写 stderr，也不等待 Kill、日志锁或 cmd.Wait。
			// 与 stop 持同一把锁，防止正常返回后已触发的回调再调用 Exit。
			exit(124)
		}
	})
	return d
}

func (d *executionDeadline) setProcess(process *os.Process) {
	d.mu.Lock()
	d.process = process
	timedOut := d.timedOut
	d.mu.Unlock()
	if timedOut {
		// 截止可能发生在 Start 内部；新 child 自带 deadline，此处只回收自己的 child。
		go process.Kill()
	}
}

func (d *executionDeadline) setTimeoutEvent(notify func()) {
	d.mu.Lock()
	d.onTimeout = notify
	d.mu.Unlock()
}

func (d *executionDeadline) stop() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	// 即使计时器回调尚未获得调度，越过执行预算的返回也必须是 124。
	if !d.stopped && !time.Now().Before(d.cutoff) {
		d.timedOut = true
	}
	d.stopped = true
	if d.soft != nil {
		d.soft.Stop()
	}
	d.hard.Stop()
	return d.timedOut
}

// runLoggedCLI 将真实 CLI 放在 child 中，以保留 os.Exit 的退出码并收集输出。
func runLoggedCLI(args []string, run func()) int {
	return runLoggedCLIWithBudget(args, run, executionScreenshotBudget)
}

// budget 仅用于进程级测试缩短等待；生产入口固定 20s（含排队），不提供环境变量开关。
func runLoggedCLIWithBudget(args []string, run func(), budget time.Duration) (code int) {
	started := time.Now()
	parentPID, parentErr := strconv.Atoi(os.Getenv(executionLogChildEnv))
	internalChild := parentErr == nil && parentPID > 0
	detachedMark := os.Getenv(executionLogDetachedEnv) == "1"
	inheritedDeadline := os.Getenv(executionLogDeadlineEnv)
	inheritedLockKey := os.Getenv(executionScreenshotLockEnv)
	lockDir := os.Getenv(executionDiagnosticDirEnv)
	_ = os.Unsetenv(executionLogChildEnv)
	_ = os.Unsetenv(executionLogDetachedEnv)
	_ = os.Unsetenv(executionLogDeadlineEnv)
	_ = os.Unsetenv(executionScreenshotLockEnv)
	command, lockKey := "", ""
	if inherited, err := strconv.ParseInt(inheritedDeadline, 10, 64); internalChild && err == nil && inherited > 0 {
		// 父进程只会在已确认为单帧截图时传递截止时间，child 无需再解析一遍文法。
		command, lockKey = "screenshot", inheritedLockKey
	} else {
		command, lockKey = executionLogInvocation(args)
	}
	detached := detachedMark && command == "keyboard daemon" && os.Getenv("GO_IOS_KEYBOARD_BACKGROUND") == "1" && os.Getenv("GO_IOS_KEYBOARD_TOKEN") != ""
	var guard *executionDeadline
	var deadline time.Time
	if command == "screenshot" {
		deadline = started.Add(budget)
		reap := executionScreenshotReap
		if internalChild {
			// child 接收父进程预算内的执行截止时间，不能重新计时或延长预算。
			if nanos, err := strconv.ParseInt(inheritedDeadline, 10, 64); err == nil {
				inherited := time.Unix(0, nanos)
				if inherited.Before(deadline) {
					deadline = inherited
				}
			}
			reap = 0
		}
		guard = newExecutionDeadline(deadline, reap, os.Exit)
		// 必须最后执行：涵盖下方所有日志 flush/Close defer。
		defer func() {
			if guard.stop() {
				code = 124
			}
		}()
	}
	if internalChild || detached {
		// detached 校验不依赖 Getppid，start 即刻退出也不能再套一层 logger。
		if !detached {
			if parentPID != os.Getppid() {
				return 124
			}
			stopWatching := watchExecutionLogParent(parentPID)
			defer stopWatching()
		}
		if dir, runID := os.Getenv(executionDiagnosticDirEnv), os.Getenv(executionDiagnosticRunIDEnv); dir != "" && runID != "" {
			_ = os.Unsetenv(executionDiagnosticDirEnv)
			_ = os.Unsetenv(executionDiagnosticRunIDEnv)
			executionDiagnosticRunID = runID
			executionDiagnosticWriter = newExecutionLogWriter(dir, runID, "diagnostic", time.Now)
			golog.EnableDiagnosticStages(true)
			defer executionDiagnosticWriter.Close()
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				ticker := time.NewTicker(time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						executionDiagnosticWriter.RollSegment()
					case <-stop:
						return
					}
				}
			}()
			slog.SetDefault(slog.New(newExecutionCLIHandler(slog.NewTextHandler(os.Stderr, nil))))
		}
		if internalChild && command == "screenshot" && lockKey != "" {
			// 同一设备的截图逐个执行；等待计入整体预算，超时与被杀都会自动释放锁。
			release, ok := acquireScreenshotQueue(lockDir, lockKey, deadline)
			if !ok {
				return 124
			}
			defer release()
		}
		run()
		return 0
	}

	dir := os.Getenv(executionLogDirEnv)
	managedDir := dir == ""
	if managedDir {
		dir = defaultExecutionLogDir(runtime.GOOS, os.TempDir(), os.Getuid())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot create execution log directory: %v\n", err)
		return 1
	}
	if info, err := os.Lstat(dir); err != nil || !validExecutionLogDir(info, runtime.GOOS) {
		fmt.Fprintln(os.Stderr, "go-ios: execution log directory must be a private real directory")
		return 1
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot initialize execution log: %v\n", err)
		return 1
	}
	runID := fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(nonce[:]))
	if managedDir {
		cleanupExecutionLogs(dir, time.Now())
	}
	events := newExecutionLogWriter(dir, runID, "events", time.Now)
	stdoutLog := newExecutionLogWriter(dir, runID, "stdout", time.Now)
	stderrLog := newExecutionLogWriter(dir, runID, "stderr", time.Now)
	defer events.Close()
	defer stdoutLog.Close()
	defer stderrLog.Close()

	if guard != nil {
		guard.setTimeoutEvent(func() {
			timeoutCode := 124
			_ = writeExecutionEvent(events, executionLogEvent{Time: time.Now(), RunID: runID, Event: "timeout", ExitCode: &timeoutCode, DurationMS: time.Since(started).Milliseconds()})
		})
	}
	if err := writeExecutionEvent(events, executionLogEvent{
		Time: started, RunID: runID, PID: os.Getpid(), Event: "start", Args: append([]string(nil), args...),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot write execution log: %v\n", err)
		return 1
	}

	executable, err := os.Executable()
	if err != nil {
		return finishExecutionLog(events, runID, started, 1, err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), executionLogChildEnv+"="+strconv.Itoa(os.Getpid()), executionDiagnosticDirEnv+"="+dir, executionDiagnosticRunIDEnv+"="+runID)
	if guard != nil {
		cmd.Env = append(cmd.Env, executionLogDeadlineEnv+"="+strconv.FormatInt(deadline.Add(-executionScreenshotReap).UnixNano(), 10), executionScreenshotLockEnv+"="+lockKey)
	}
	cmd.Stdout = &executionMirror{console: os.Stdout, log: stdoutLog}
	cmd.Stderr = &executionMirror{console: os.Stderr, log: stderrLog}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot start command: %v\n", err)
		return finishExecutionLog(events, runID, started, 1, err)
	}
	if guard != nil {
		guard.setProcess(cmd.Process)
	}
	if err := writeExecutionEvent(events, executionLogEvent{Time: time.Now(), RunID: runID, PID: cmd.Process.Pid, Event: "child_started"}); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot write child process event: %v\n", err)
	}
	done := make(chan struct{})
	maintenance := time.NewTicker(time.Hour)
	heartbeat := time.NewTicker(5 * time.Second)
	defer maintenance.Stop()
	defer heartbeat.Stop()
	go func() {
		for {
			select {
			case <-heartbeat.C:
				if err := writeExecutionEvent(events, executionLogEvent{Time: time.Now(), RunID: runID, Event: "running", DurationMS: time.Since(started).Milliseconds()}); err != nil {
					fmt.Fprintf(os.Stderr, "go-ios: execution heartbeat log failed: %v\n", err)
				}
			case <-maintenance.C:
				events.RollSegment()
				stdoutLog.RollSegment()
				stderrLog.RollSegment()
				if managedDir {
					cleanupExecutionLogs(dir, time.Now())
				}
			case <-done:
				return
			}
		}
	}()
	// On Windows the console delivers Ctrl+C to both processes; sending
	// os.Interrupt via Process.Signal is unsupported. Keep the parent alive
	// until Wait drains the child output. On Unix, forward signals sent only
	// to the parent (for example by an external automation runner).
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	go func() {
		select {
		case sig := <-interrupts:
			if runtime.GOOS != "windows" {
				_ = cmd.Process.Signal(sig)
			}
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	code = 0
	if err != nil {
		code = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			code = exitErr.ExitCode()
		}
	}
	if guard != nil {
		guard.mu.Lock()
		if guard.timedOut {
			code = 124
		}
		guard.mu.Unlock()
	}
	return finishExecutionLog(events, runID, started, code, err)
}

func finishExecutionLog(events *executionLogWriter, runID string, started time.Time, code int, err error) int {
	entry := executionLogEvent{
		Time: time.Now(), RunID: runID, PID: os.Getpid(), Event: "finish", ExitCode: &code,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if err != nil {
		entry.Error = err.Error()
	}
	if writeErr := writeExecutionEvent(events, entry); writeErr != nil {
		fmt.Fprintf(os.Stderr, "go-ios: cannot write execution log: %v\n", writeErr)
	}
	return code
}

func writeExecutionEvent(w io.Writer, event executionLogEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if writer, ok := w.(*executionLogWriter); ok {
		_, err = writer.WriteRecord(data)
	} else {
		_, err = w.Write(data)
	}
	return err
}

// executionMirror never lets a logging failure suppress terminal output.
// The stream files contain the exact bytes sent to stdout/stderr, including
// binary screenshot/video streams; events are kept separately as JSON lines.
type executionMirror struct {
	console io.Writer
	log     *executionLogWriter
	warned  bool
}

func (m *executionMirror) Write(p []byte) (int, error) {
	n, err := m.console.Write(p)
	if n > 0 {
		if _, logErr := m.log.Write(p[:n]); logErr != nil && !m.warned {
			m.warned = true
			fmt.Fprintf(os.Stderr, "go-ios: execution output log failed: %v\n", logErr)
		}
	}
	return n, err
}

type executionLogWriter struct {
	mu     sync.Mutex
	dir    string
	runID  string
	stream string
	now    func() time.Time
	file   *os.File
	day    string
	seq    int
	size   int64
}

func newExecutionLogWriter(dir, runID, stream string, now func() time.Time) *executionLogWriter {
	return &executionLogWriter{dir: dir, runID: runID, stream: stream, now: now}
}

// WriteRecord keeps one JSON event on a single side of a size boundary.
func (w *executionLogWriter) WriteRecord(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.open(w.now()); err != nil {
		return 0, err
	}
	if w.size > 0 && w.size+int64(len(p)) > executionLogMaxSize {
		_ = w.file.Close()
		w.file = nil
		if err := w.open(w.now()); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *executionLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := 0
	for len(p) > 0 {
		if err := w.open(w.now()); err != nil {
			return written, err
		}
		part := int64(len(p))
		if remaining := int64(executionLogMaxSize) - w.size; part > remaining {
			part = remaining
		}
		n, err := w.file.Write(p[:int(part)])
		written += n
		w.size += int64(n)
		p = p[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *executionLogWriter) open(now time.Time) error {
	day := now.UTC().Format("20060102")
	if w.file != nil && (w.day != day || w.size >= executionLogMaxSize) {
		_ = w.file.Close()
		w.file = nil
	}
	if w.file != nil {
		return nil
	}
	if w.day != day {
		w.day, w.seq = day, 0
	}
	w.seq++
	path := filepath.Join(w.dir, fmt.Sprintf("go-ios-%s-%s-%s-%06d.log", day, w.runID, w.stream, w.seq))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.size = info.Size()
	w.file = file
	return nil
}

func (w *executionLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Closing active segments hourly ensures retention cleanup cannot unlink a
// still-open file from a long-running command. The next write appends to a new
// numbered segment, with the UTC date refreshed as needed.
func (w *executionLogWriter) RollSegment() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

// Only files generated by this feature, directly in its dedicated directory,
// are eligible. Never descend into directories or follow symbolic links.
func cleanupExecutionLogs(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-executionLogLifetime)
	for _, entry := range entries {
		if !executionLogName.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
	}
}
