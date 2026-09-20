package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/fileservice"
	"github.com/google/uuid"
)

const (
	testmanagerdBundleID       = "com.apple.testmanagerd"
	testmanagerdAttachmentsDir = "tmp/Attachments"
)

type testmanagerdFileService interface {
	ListDirectory(path string) ([]string, error)
	PullFile(path string, writer io.Writer) error
	RemoveFile(path string) error
	Close() error
}

type testmanagerdCommandDeps struct {
	goos           string
	run            func(name string, args ...string) ([]byte, error)
	newFileService func(device ios.DeviceEntry) (testmanagerdFileService, error)
	mkdirTemp      func(dir, pattern string) (string, error)
	removeAll      func(path string) error
	mkdirAll       func(path string, perm os.FileMode) error
	openExclusive  func(path string) (*os.File, error)
	stat           func(path string) (os.FileInfo, error)
	abs            func(path string) (string, error)
	link           func(oldname, newname string) error
}

type deleteAllTestmanagerdScreenrecordResult struct {
	UDID         string `json:"udid"`
	RemotePath   string `json:"remotePath"`
	Transport    string `json:"transport"`
	DeletedCount int    `json:"deletedCount,omitempty"`
	Output       string `json:"output,omitempty"`
}

type pullTestmanagerdScreenrecordResult struct {
	UDID       string `json:"udid"`
	VideoID    string `json:"videoId"`
	RemotePath string `json:"remotePath"`
	LocalPath  string `json:"localPath"`
	Transport  string `json:"transport"`
	Output     string `json:"output,omitempty"`
}

func defaultTestmanagerdCommandDeps() testmanagerdCommandDeps {
	return testmanagerdCommandDeps{
		goos: runtime.GOOS,
		run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		newFileService: func(device ios.DeviceEntry) (testmanagerdFileService, error) {
			return fileservice.New(device, fileservice.DomainAppDataContainer, testmanagerdBundleID)
		},
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		mkdirAll:  os.MkdirAll,
		openExclusive: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		},
		stat: os.Stat,
		abs:  filepath.Abs,
		link: os.Link,
	}
}

func runDeleteAllTestmanagerdScreenrecordCommand(ctx commandContext) {
	if !ctx.Device.SupportsRsd() {
		exitIfError("delete_all_testmanagerd_screenrecord", fmt.Errorf("native CoreDevice fileservice requires iOS 17+ with an active tunnel"))
	}

	result, err := deleteAllTestmanagerdScreenrecordNative(ctx.Device, defaultTestmanagerdCommandDeps())
	exitIfError("delete_all_testmanagerd_screenrecord", err)

	if JSONdisabled {
		fmt.Printf("Deleted %d testmanagerd screen recording attachments from %s on device %s using %s\n", result.DeletedCount, result.RemotePath, result.UDID, result.Transport)
		return
	}
	fmt.Println(convertToJSONString(result))
}

func runPullTestmanagerdScreenrecordCommand(ctx commandContext) {
	udid := strings.TrimSpace(ctx.Device.Properties.SerialNumber)
	if udid == "" {
		exitIfError("pull_testmanagerd_screenrecord", fmt.Errorf("device UDID is required"))
	}

	videoID, _ := ctx.Args.String("--video-id")
	outputDir, _ := ctx.Args.String("--output-dir")
	deps := defaultTestmanagerdCommandDeps()
	var result pullTestmanagerdScreenrecordResult
	var err error
	if ctx.Device.SupportsRsd() {
		result, err = pullTestmanagerdScreenrecordNative(ctx.Device, videoID, outputDir, deps)
		if err != nil && runtime.GOOS == "darwin" {
			slog.Warn("Native fileservice pull failed; falling back to macOS devicectl", "error", err)
			result, err = pullTestmanagerdScreenrecord(udid, videoID, outputDir, deps)
		}
	} else {
		result, err = pullTestmanagerdScreenrecord(udid, videoID, outputDir, deps)
	}
	exitIfError("pull_testmanagerd_screenrecord", err)

	if JSONdisabled {
		fmt.Printf("Pulled %s to %s using %s\n", result.RemotePath, result.LocalPath, result.Transport)
		return
	}
	fmt.Println(convertToJSONString(result))
}

func deleteAllTestmanagerdScreenrecordNative(device ios.DeviceEntry, deps testmanagerdCommandDeps) (deleteAllTestmanagerdScreenrecordResult, error) {
	result := deleteAllTestmanagerdScreenrecordResult{
		UDID:       strings.TrimSpace(device.Properties.SerialNumber),
		RemotePath: testmanagerdAttachmentsDir,
		Transport:  "fileservice",
	}
	if result.UDID == "" {
		return result, fmt.Errorf("device UDID is required")
	}
	if deps.newFileService == nil {
		return result, fmt.Errorf("fileservice factory is unavailable")
	}

	entries, err := listTestmanagerdAttachmentsNative(device, deps)
	if err != nil {
		return result, fmt.Errorf("list testmanagerd screen recording attachments: %w", err)
	}
	targets, err := normalizeTestmanagerdAttachmentPaths(entries)
	if err != nil {
		return result, err
	}
	var operationErr error
	if len(targets) != 0 {
		deleteConn, err := deps.newFileService(device)
		if err != nil {
			return result, fmt.Errorf("connect to native CoreDevice fileservice for deletion: %w", err)
		}
		for _, target := range targets {
			if err := deleteConn.RemoveFile(target); err != nil {
				operationErr = fmt.Errorf("remove testmanagerd screen recording %q after deleting %d file(s): %w", target, result.DeletedCount, err)
				break
			}
			result.DeletedCount++
		}
		if err := deleteConn.Close(); err != nil {
			slog.Warn("Failed to close native CoreDevice fileservice after deletion; continuing with verification", "error", err)
		}
	}

	remaining, err := listTestmanagerdAttachmentsNative(device, deps)
	if err != nil {
		if operationErr != nil {
			return result, fmt.Errorf("%v; cleanup verification also failed: %w", operationErr, err)
		}
		return result, fmt.Errorf("verify testmanagerd screen recording cleanup: %w", err)
	}
	remainingTargets, err := normalizeTestmanagerdAttachmentPaths(remaining)
	if err != nil {
		if operationErr != nil {
			return result, fmt.Errorf("%v; cleanup verification also failed: %w", operationErr, err)
		}
		return result, fmt.Errorf("verify testmanagerd screen recording cleanup: %w", err)
	}
	if len(remainingTargets) != 0 {
		if operationErr != nil {
			return result, fmt.Errorf("%v; cleanup verification found remaining attachment: %s", operationErr, remainingTargets[0])
		}
		return result, fmt.Errorf("testmanagerd screen recording attachment still exists: %s", remainingTargets[0])
	}
	if operationErr != nil {
		result.Output = operationErr.Error()
		slog.Warn("Native fileservice reported a deletion error, but independent verification confirmed the attachment directory is empty", "error", operationErr)
	}
	return result, nil
}

func listTestmanagerdAttachmentsNative(device ios.DeviceEntry, deps testmanagerdCommandDeps) ([]string, error) {
	conn, err := deps.newFileService(device)
	if err != nil {
		return nil, fmt.Errorf("connect to native CoreDevice fileservice: %w", err)
	}
	entries, listErr := conn.ListDirectory(testmanagerdAttachmentsDir)
	closeErr := conn.Close()
	if listErr != nil {
		return nil, listErr
	}
	if closeErr != nil {
		slog.Warn("Failed to close native CoreDevice fileservice after listing", "error", closeErr)
	}
	return entries, nil
}

func normalizeTestmanagerdAttachmentPaths(entries []string) ([]string, error) {
	targets := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(entry, "\\") {
			return nil, fmt.Errorf("refusing to normalize attachment path containing a backslash: %q", entry)
		}
		normalized := strings.TrimPrefix(entry, "./")
		if normalized == "" || normalized == "." || normalized == testmanagerdAttachmentsDir {
			continue
		}
		if !strings.Contains(normalized, "/") {
			normalized = path.Join(testmanagerdAttachmentsDir, normalized)
		}
		if path.Clean(normalized) != normalized || !strings.HasPrefix(normalized, testmanagerdAttachmentsDir+"/") {
			return nil, fmt.Errorf("fileservice returned an attachment path outside %s: %q", testmanagerdAttachmentsDir, entry)
		}
		relative := strings.TrimPrefix(normalized, testmanagerdAttachmentsDir+"/")
		if !isCanonicalUUID(relative) {
			return nil, fmt.Errorf("refusing to remove non-UUID attachment entry: %q", entry)
		}
		targets = append(targets, normalized)
	}
	return targets, nil
}

func pullTestmanagerdScreenrecordNative(device ios.DeviceEntry, videoID, outputDir string, deps testmanagerdCommandDeps) (pullTestmanagerdScreenrecordResult, error) {
	result, stagingDir, stagingPath, err := prepareTestmanagerdScreenrecordPull(device.Properties.SerialNumber, videoID, outputDir, "fileservice", deps)
	if err != nil {
		return result, err
	}
	defer cleanupTestmanagerdStagingDirectory(stagingDir, deps)

	if deps.newFileService == nil {
		return result, fmt.Errorf("fileservice factory is unavailable")
	}
	conn, err := deps.newFileService(device)
	if err != nil {
		return result, fmt.Errorf("connect to native CoreDevice fileservice: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Warn("Failed to close testmanagerd fileservice connection", "error", closeErr)
		}
	}()

	outputFile, err := deps.openExclusive(stagingPath)
	if err != nil {
		return result, fmt.Errorf("create staging file %q: %w", stagingPath, err)
	}
	pullErr := conn.PullFile(result.RemotePath, outputFile)
	closeErr := outputFile.Close()
	if pullErr != nil {
		return result, fmt.Errorf("native fileservice pull failed and the temporary output was removed without retry: %w", pullErr)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close pulled screen recording %q: %w", stagingPath, closeErr)
	}
	if err := publishTestmanagerdScreenrecord(result, stagingPath, deps); err != nil {
		return result, err
	}
	return result, nil
}

func pullTestmanagerdScreenrecord(udid, videoID, outputDir string, deps testmanagerdCommandDeps) (pullTestmanagerdScreenrecordResult, error) {
	result := pullTestmanagerdScreenrecordResult{UDID: strings.TrimSpace(udid), Transport: "devicectl"}
	if err := validateTestmanagerdPlatform(deps.goos); err != nil {
		return result, err
	}
	result, stagingDir, stagingPath, err := prepareTestmanagerdScreenrecordPull(udid, videoID, outputDir, "devicectl", deps)
	if err != nil {
		return result, err
	}
	defer cleanupTestmanagerdStagingDirectory(stagingDir, deps)

	args := pullTestmanagerdScreenrecordArgs(result.UDID, result.RemotePath, stagingPath)
	output, err := deps.run("xcrun", args...)
	result.Output = strings.TrimSpace(string(output))
	if err != nil {
		return result, fmt.Errorf("pull testmanagerd screen recording failed and the temporary output was removed without retry: %w; output: %s", err, result.Output)
	}
	if err := publishTestmanagerdScreenrecord(result, stagingPath, deps); err != nil {
		return result, err
	}
	return result, nil
}

func prepareTestmanagerdScreenrecordPull(udid, videoID, outputDir, transport string, deps testmanagerdCommandDeps) (pullTestmanagerdScreenrecordResult, string, string, error) {
	result := pullTestmanagerdScreenrecordResult{
		UDID:      strings.TrimSpace(udid),
		Transport: transport,
	}
	if result.UDID == "" {
		return result, "", "", fmt.Errorf("device UDID is required")
	}

	videoID = strings.TrimSpace(videoID)
	if _, err := uuid.Parse(videoID); err != nil || !isCanonicalUUID(videoID) {
		return result, "", "", fmt.Errorf("--video-id must be a canonical UUID: %q", videoID)
	}
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return result, "", "", fmt.Errorf("--output-dir is required")
	}
	if err := deps.mkdirAll(outputDir, 0o755); err != nil {
		return result, "", "", fmt.Errorf("create output directory %q: %w", outputDir, err)
	}
	absoluteOutputDir, err := deps.abs(outputDir)
	if err != nil {
		return result, "", "", fmt.Errorf("resolve output directory %q: %w", outputDir, err)
	}

	result.VideoID = videoID
	result.RemotePath = testmanagerdAttachmentsDir + "/" + videoID
	result.LocalPath = filepath.Join(absoluteOutputDir, videoID+".mov")
	if _, err := deps.stat(result.LocalPath); err == nil {
		return result, "", "", fmt.Errorf("destination already exists: %s", result.LocalPath)
	} else if !os.IsNotExist(err) {
		return result, "", "", fmt.Errorf("inspect destination %q: %w", result.LocalPath, err)
	}

	stagingDir, err := deps.mkdirTemp(absoluteOutputDir, ".go-ios-testmanagerd-pull-*")
	if err != nil {
		return result, "", "", fmt.Errorf("create staging directory in %q: %w", absoluteOutputDir, err)
	}
	return result, stagingDir, filepath.Join(stagingDir, videoID+".mov"), nil
}

func publishTestmanagerdScreenrecord(result pullTestmanagerdScreenrecordResult, stagingPath string, deps testmanagerdCommandDeps) error {
	fileInfo, err := deps.stat(stagingPath)
	if err != nil {
		return fmt.Errorf("%s completed but the pulled screen recording is unavailable at %q: %w", result.Transport, stagingPath, err)
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("%s destination is not a regular file: %s", result.Transport, stagingPath)
	}
	if fileInfo.Size() == 0 {
		return fmt.Errorf("%s created an empty screen recording: %s", result.Transport, stagingPath)
	}
	if err := deps.link(stagingPath, result.LocalPath); err != nil {
		if _, statErr := deps.stat(result.LocalPath); statErr == nil {
			return fmt.Errorf("destination already exists: %s", result.LocalPath)
		}
		return fmt.Errorf("publish pulled screen recording to %q: %w", result.LocalPath, err)
	}
	return nil
}

func cleanupTestmanagerdStagingDirectory(stagingDir string, deps testmanagerdCommandDeps) {
	if cleanupErr := deps.removeAll(stagingDir); cleanupErr != nil {
		slog.Warn("Failed to remove temporary testmanagerd pull directory", "path", stagingDir, "error", cleanupErr)
	}
}

func validateTestmanagerdPlatform(goos string) error {
	if goos != "darwin" {
		return fmt.Errorf("testmanagerd screen recording commands are only supported on macOS with Xcode xcrun devicectl (current platform: %s)", goos)
	}
	return nil
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
				return false
			}
		}
	}
	return true
}

func pullTestmanagerdScreenrecordArgs(udid, remotePath, localPath string) []string {
	return []string{
		"devicectl", "device", "copy", "from",
		"--device", udid,
		"--domain-type", "appDataContainer",
		"--domain-identifier", testmanagerdBundleID,
		"--user", "mobile",
		"--source", remotePath,
		"--destination", localPath,
		"--quiet",
	}
}
