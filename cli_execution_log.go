package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/golog"
	"github.com/docopt/docopt-go"
)

// 执行日志设计目标：命令快速返回、日志快速落盘。
//
//   - 进程内直接写文件，不派生 child 进程、不镜像 stdout/stderr。
//   - 按「设备 + 本地日期」归档：go-ios-<udid>-YYYYMMDD.log；未指定 -u 的命令写入
//     go-ios-default-YYYYMMDD.log；单文件超过上限后续写 go-ios-<udid>-YYYYMMDD-<n>.log。
//   - 每条记录一次 O_APPEND write，不做用户态缓冲：os.Exit、超时 124、被 kill -9
//     时已写出的记录都已进入内核；同设备多个进程并发追加也不会互相覆盖。
//   - 过期清理每小时最多触发一次，并在后台协程中执行，不阻塞命令。
const (
	executionLogDirEnv          = "GO_IOS_LOG_DIR"
	executionLogLevelEnv        = "GO_IOS_LOG_LEVEL" // trace|debug|info|warn|error|off，默认 info
	executionLogMaxSize         = 64 << 20
	executionLogLifetime        = 72 * time.Hour
	executionLogCleanupInterval = time.Hour
	executionLogCleanupStamp    = ".cleanup-stamp"
	executionLogDefaultKey      = "default"
	executionLogStatEvery       = 256 // 每写 N 条刷新一次真实文件大小（其他进程也在追加）
	// 同一设备的 screenshot 串行排队；从进程启动开始计时，排队等待与执行共用这一个整体预算。
	executionScreenshotBudget = 20 * time.Second
	executionTimeoutExitCode  = 124
)

var (
	executionLogName       = regexp.MustCompile(`^go-ios-[A-Za-z0-9._-]+-\d{8}(-\d+)?\.log$`)
	legacyExecutionLogName = regexp.MustCompile(`^go-ios-\d{8}-\d+-[0-9a-f]{16}-(stdout|stderr|events|diagnostic)-\d{6}\.log$`)
	executionLogKeyUnsafe  = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

var (
	executionStarted     time.Time
	executionLogDir      string       // 可用的私有日志目录，同时用作截图队列锁目录；"" 表示不可用
	executionFileHandler slog.Handler // nil 表示文件日志关闭
	executionFinished    atomic.Bool  // 保证 finish/timeout 只记录一次
	screenshotDeadline   time.Time
	screenshotTimer      *time.Timer
)

// startExecutionLog 在解析命令之前调用：只做一次 MkdirAll/Lstat，日志文件惰性打开，
// 并写入 start 记录。日志不可用时命令照常执行。
func startExecutionLog(args []string) {
	executionStarted = time.Now()
	dir := os.Getenv(executionLogDirEnv)
	managedDir := dir == ""
	if managedDir {
		dir = defaultExecutionLogDir(runtime.GOOS, os.TempDir(), os.Getuid())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "go-ios: execution log disabled: %v\n", err)
		return
	}
	if info, err := os.Lstat(dir); err != nil || !validExecutionLogDir(info, runtime.GOOS) {
		fmt.Fprintln(os.Stderr, "go-ios: execution log disabled: directory must be a private real directory")
		return
	}
	executionLogDir = dir
	if managedDir {
		maybeCleanupExecutionLogs(dir, time.Now())
		go executionLogMaintenance(dir)
	}
	level, enabled := parseExecutionLogLevel(os.Getenv(executionLogLevelEnv))
	if !enabled {
		return
	}
	out := newExecutionLogFile(dir, executionLogKey(args, os.Getenv("GO_IOS_UDID")))
	executionFileHandler = slog.NewTextHandler(out, &slog.HandlerOptions{
		Level: level, ReplaceAttr: replaceTraceLevel,
	}).WithAttrs([]slog.Attr{slog.Int("pid", os.Getpid())})
	golog.EnableDiagnosticStages(true)
	// configureCLI 会按 --nojson/--verbose 替换控制台部分，文件部分保持不变。
	slog.SetDefault(slog.New(newExecutionCLIHandler(slog.NewTextHandler(os.Stderr, nil))))
	writeExecutionLifecycle("command start", slog.Any("args", args))
}

// finishExecutionLog 记录退出码与耗时，返回最终退出码（越过截图预算的返回统一为 124）。
func finishExecutionLog(code int) int {
	if !executionFinished.CompareAndSwap(false, true) {
		return code
	}
	if screenshotTimer != nil {
		screenshotTimer.Stop()
		if !time.Now().Before(screenshotDeadline) {
			code = executionTimeoutExitCode
		}
	}
	writeExecutionLifecycle("command finish", slog.Int("exit_code", code), slog.Int64("duration_ms", time.Since(executionStarted).Milliseconds()))
	return code
}

// exitCLI 替代 os.Exit：先写 finish 记录再退出。
func exitCLI(code int) {
	os.Exit(finishExecutionLog(code))
}

// recordExecutionPanic 在主协程 panic 时补记一条带堆栈的记录，然后继续 panic。
func recordExecutionPanic() {
	if r := recover(); r != nil {
		if executionFinished.CompareAndSwap(false, true) {
			writeExecutionLifecycle("command panic", slog.Int("exit_code", 2), slog.Int64("duration_ms", time.Since(executionStarted).Milliseconds()),
				slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
		}
		panic(r)
	}
}

func writeExecutionLifecycle(msg string, attrs ...slog.Attr) {
	if executionFileHandler == nil {
		return
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	record.AddAttrs(attrs...)
	// 生命周期记录不受 GO_IOS_LOG_LEVEL 过滤（off 除外），直接交给文件 handler。
	_ = executionFileHandler.Handle(context.Background(), record)
}

// armScreenshotDeadline 只对单帧截图生效：从进程启动起 20s 内必须结束，否则退出 124。
// 计时器在独立协程触发，即使主协程阻塞在设备 I/O 上也能按时退出。
func armScreenshotDeadline(arguments docopt.Opts) {
	if !boolArg(arguments, "screenshot") || boolArg(arguments, "ui") || boolArg(arguments, "--stream") {
		return
	}
	started := executionStarted
	if started.IsZero() {
		started = time.Now()
	}
	screenshotDeadline = started.Add(executionScreenshotBudget)
	screenshotTimer = time.AfterFunc(time.Until(screenshotDeadline), func() {
		if !executionFinished.CompareAndSwap(false, true) {
			return
		}
		writeExecutionLifecycle("command timeout", slog.Int("exit_code", executionTimeoutExitCode), slog.Int64("duration_ms", time.Since(executionStarted).Milliseconds()))
		os.Exit(executionTimeoutExitCode)
	})
}

func parseExecutionLogLevel(value string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "none", "disable", "disabled":
		return 0, false
	case "trace":
		return ios.LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, true
	}
}

// executionLogKey 不经 docopt，直接从原始参数中取 -u（或 GO_IOS_UDID）作为文件键。
func executionLogKey(args []string, envUDID string) string {
	udid := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "-u" && i+1 < len(args) {
			udid = args[i+1]
			break
		}
		if strings.HasPrefix(arg, "-u=") {
			udid = arg[len("-u="):]
			break
		}
	}
	if udid == "" {
		udid = envUDID
	}
	udid = executionLogKeyUnsafe.ReplaceAllString(strings.TrimSpace(udid), "_")
	if len(udid) > 80 {
		udid = udid[:80]
	}
	if udid == "" {
		return executionLogDefaultKey
	}
	return udid
}

// diagnosticInfo is reserved for file-only stage records; normal slog calls
// still follow the user's existing console level and format.
func diagnosticInfo(msg string, args ...any)  { diagnosticStage(slog.LevelInfo, msg, args...) }
func diagnosticWarn(msg string, args ...any)  { diagnosticStage(slog.LevelWarn, msg, args...) }
func diagnosticError(msg string, args ...any) { diagnosticStage(slog.LevelError, msg, args...) }

func diagnosticStage(level slog.Level, msg string, args ...any) {
	if executionFileHandler == nil {
		return
	}
	ctx := context.Background()
	if !executionFileHandler.Enabled(ctx, level) {
		return
	}
	// 阶段记录只进文件，直接交给文件 handler，省去控制台分流判断。
	record := slog.NewRecord(time.Now(), level, msg, 0)
	record.Add(args...)
	_ = executionFileHandler.Handle(ctx, record)
}

// executionDiagnosticHandler 把记录同时交给控制台与文件：文件按 GO_IOS_LOG_LEVEL
// 过滤（默认 Info），控制台保持用户原有级别；diagnostic-only 的阶段记录只进文件。
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
			return false
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

func newExecutionCLIHandler(console slog.Handler) slog.Handler {
	return &executionDiagnosticHandler{console: console, file: executionFileHandler}
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

// executionLogFile 是按「设备 + 本地日期」切分的追加写文件。slog handler 每条记录
// 只调用一次 Write，这里也只发出一次 write 系统调用，不做缓冲。
type executionLogFile struct {
	mu      sync.Mutex
	dir     string
	key     string
	file    *os.File
	day     string
	rollAt  time.Time
	seq     int
	size    int64
	records int
	failed  bool
}

func newExecutionLogFile(dir, key string) *executionLogFile {
	return &executionLogFile{dir: dir, key: key}
}

func (w *executionLogFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return len(p), nil
	}
	now := time.Now()
	if w.file != nil && !now.Before(w.rollAt) {
		w.closeLocked()
	}
	if w.file != nil && w.size+int64(len(p)) > executionLogMaxSize {
		w.refreshSizeLocked()
		if w.size+int64(len(p)) > executionLogMaxSize {
			w.closeLocked()
			w.seq++
		}
	}
	if w.file == nil {
		if err := w.openLocked(now); err != nil {
			// 日志不可写时不再重试，也不影响命令本身。
			w.failed = true
			return len(p), nil
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	w.records++
	if w.records%executionLogStatEvery == 0 {
		w.refreshSizeLocked()
	}
	return n, err
}

func (w *executionLogFile) path(day string, seq int) string {
	if seq == 0 {
		return filepath.Join(w.dir, fmt.Sprintf("go-ios-%s-%s.log", w.key, day))
	}
	return filepath.Join(w.dir, fmt.Sprintf("go-ios-%s-%s-%d.log", w.key, day, seq))
}

func (w *executionLogFile) openLocked(now time.Time) error {
	day := now.Format("20060102")
	if day != w.day {
		y, m, d := now.Date()
		w.day, w.seq = day, 0
		w.rollAt = time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	}
	// 跳过已写满的分段；分段数很少，逐个 Stat 即可，不读取目录。
	for {
		info, err := os.Stat(w.path(day, w.seq))
		if err != nil || info.Size() < executionLogMaxSize {
			break
		}
		w.seq++
	}
	file, err := os.OpenFile(w.path(day, w.seq), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.refreshSizeLocked()
	return nil
}

func (w *executionLogFile) refreshSizeLocked() {
	if w.file == nil {
		return
	}
	if info, err := w.file.Stat(); err == nil {
		w.size = info.Size()
	}
}

func (w *executionLogFile) closeLocked() {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

// 长时间运行的命令（runwda、tunnel、keyboard daemon 等）每小时尝试一次过期清理。
func executionLogMaintenance(dir string) {
	ticker := time.NewTicker(executionLogCleanupInterval)
	defer ticker.Stop()
	for now := range ticker.C {
		maybeCleanupExecutionLogs(dir, now)
	}
}

// maybeCleanupExecutionLogs 用时间戳文件节流：距上次清理不足 1 小时直接返回（一次 Lstat）；
// 否则先刷新时间戳占位，再在后台协程中清理，命令本身不等待。
func maybeCleanupExecutionLogs(dir string, now time.Time) {
	stamp := filepath.Join(dir, executionLogCleanupStamp)
	if info, err := os.Lstat(stamp); err == nil {
		if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < executionLogCleanupInterval {
			return
		}
		if err := os.Chtimes(stamp, now, now); err != nil {
			return
		}
	} else {
		file, err := os.OpenFile(stamp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return // 其他进程刚刚创建，由它负责本轮清理
		}
		_ = file.Close()
	}
	go cleanupExecutionLogs(dir, now)
}

// Only files generated by this feature, directly in its dedicated directory,
// are eligible. Never descend into directories or follow symbolic links.
// 分批读取目录并边读边删，进程提前退出时已完成的部分仍然有效。
func cleanupExecutionLogs(dir string, now time.Time) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	cutoff := now.Add(-executionLogLifetime)
	for {
		entries, err := d.ReadDir(512)
		for _, entry := range entries {
			name := entry.Name()
			if !entry.Type().IsRegular() || !(executionLogName.MatchString(name) || legacyExecutionLogName.MatchString(name)) {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
		if err != nil {
			return
		}
	}
}

// screenshotQueueKey 使用解析后的真实 UDID，未指定 -u 时自动选中的设备也能正确排队。
func screenshotQueueKey(device ios.DeviceEntry) string {
	if udid := device.Properties.SerialNumber; udid != "" {
		return udid
	}
	return executionLogDefaultKey
}
