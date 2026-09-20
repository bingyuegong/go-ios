package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bingyuegong/go-ios/ios"
)

type fakeTestmanagerdFileService struct {
	listResponses [][]string
	removed       []string
	pullData      []byte
	removeErr     error
	closed        bool
}

func (f *fakeTestmanagerdFileService) ListDirectory(string) ([]string, error) {
	if len(f.listResponses) == 0 {
		return nil, errors.New("unexpected ListDirectory call")
	}
	result := f.listResponses[0]
	f.listResponses = f.listResponses[1:]
	return result, nil
}

func (f *fakeTestmanagerdFileService) PullFile(_ string, writer io.Writer) error {
	_, err := writer.Write(f.pullData)
	return err
}

func (f *fakeTestmanagerdFileService) RemoveFile(path string) error {
	f.removed = append(f.removed, path)
	return f.removeErr
}

func (f *fakeTestmanagerdFileService) Close() error {
	f.closed = true
	return nil
}

func TestDeleteAllTestmanagerdScreenrecordNativeRemovesEachListedFile(t *testing.T) {
	firstVideoID := "25CC55CC-C8E3-4E87-9515-3D02508B6F35"
	secondVideoID := "11111111-2222-4333-8444-555555555555"
	listService := &fakeTestmanagerdFileService{listResponses: [][]string{{"tmp/Attachments/" + firstVideoID, secondVideoID}}}
	deleteService := &fakeTestmanagerdFileService{}
	verifyService := &fakeTestmanagerdFileService{listResponses: [][]string{{}}}
	services := []testmanagerdFileService{listService, deleteService, verifyService}
	deps := testmanagerdDepsForTest(t, t.TempDir(), nil)
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		if len(services) == 0 {
			t.Fatal("unexpected fileservice connection")
		}
		service := services[0]
		services = services[1:]
		return service, nil
	}
	device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: "device-1"}}

	result, err := deleteAllTestmanagerdScreenrecordNative(device, deps)
	if err != nil {
		t.Fatalf("deleteAllTestmanagerdScreenrecordNative: %v", err)
	}
	wantRemoved := []string{"tmp/Attachments/" + firstVideoID, "tmp/Attachments/" + secondVideoID}
	if !reflect.DeepEqual(deleteService.removed, wantRemoved) {
		t.Fatalf("removed = %#v, want %#v", deleteService.removed, wantRemoved)
	}
	if result.Transport != "fileservice" || result.DeletedCount != 2 {
		t.Fatalf("result = %+v", result)
	}
	if !listService.closed || !deleteService.closed || !verifyService.closed {
		t.Fatal("one or more fileservice connections were not closed")
	}
}

func TestDeleteAllTestmanagerdScreenrecordNativeVerifiesAfterAmbiguousRemoveError(t *testing.T) {
	videoID := "25CC55CC-C8E3-4E87-9515-3D02508B6F35"
	listService := &fakeTestmanagerdFileService{listResponses: [][]string{{videoID}}}
	deleteService := &fakeTestmanagerdFileService{removeErr: errors.New("response stream closed")}
	verifyService := &fakeTestmanagerdFileService{listResponses: [][]string{{}}}
	services := []testmanagerdFileService{listService, deleteService, verifyService}
	deps := testmanagerdDepsForTest(t, t.TempDir(), nil)
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		service := services[0]
		services = services[1:]
		return service, nil
	}
	device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: "device-1"}}

	result, err := deleteAllTestmanagerdScreenrecordNative(device, deps)
	if err != nil {
		t.Fatalf("independently verified deletion returned error: %v", err)
	}
	if !strings.Contains(result.Output, "response stream closed") {
		t.Fatalf("result output = %q, want original ambiguous error", result.Output)
	}
}

func TestNormalizeTestmanagerdAttachmentPathsRejectsBackslash(t *testing.T) {
	_, err := normalizeTestmanagerdAttachmentPaths([]string{`tmp\\Attachments\\25CC55CC-C8E3-4E87-9515-3D02508B6F35`})
	if err == nil || !strings.Contains(err.Error(), "backslash") {
		t.Fatalf("error = %v, want backslash rejection", err)
	}
}

func TestDeleteAllTestmanagerdScreenrecordNativeRejectsPathOutsideAttachments(t *testing.T) {
	service := &fakeTestmanagerdFileService{listResponses: [][]string{{"tmp/other-file"}}}
	deps := testmanagerdDepsForTest(t, t.TempDir(), nil)
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) { return service, nil }
	device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: "device-1"}}

	_, err := deleteAllTestmanagerdScreenrecordNative(device, deps)
	if err == nil || !strings.Contains(err.Error(), "outside tmp/Attachments") {
		t.Fatalf("error = %v, want out-of-scope path rejection", err)
	}
	if len(service.removed) != 0 {
		t.Fatalf("removed unexpected paths: %#v", service.removed)
	}
}

func TestPullTestmanagerdScreenrecordNativePublishesNonEmptyMovie(t *testing.T) {
	service := &fakeTestmanagerdFileService{pullData: []byte("movie")}
	outputDir := filepath.Join(t.TempDir(), "videos")
	deps := testmanagerdDepsForTest(t, t.TempDir(), nil)
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) { return service, nil }
	device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: "device-1"}}
	videoID := "25CC55CC-C8E3-4E87-9515-3D02508B6F35"

	result, err := pullTestmanagerdScreenrecordNative(device, videoID, outputDir, deps)
	if err != nil {
		t.Fatalf("pullTestmanagerdScreenrecordNative: %v", err)
	}
	if result.Transport != "fileservice" {
		t.Fatalf("transport = %q, want fileservice", result.Transport)
	}
	contents, err := os.ReadFile(result.LocalPath)
	if err != nil || string(contents) != "movie" {
		t.Fatalf("published contents = %q, error = %v", contents, err)
	}
	if !service.closed {
		t.Fatal("fileservice connection was not closed")
	}
}

func TestDevicectlPullRejectsUnsupportedPlatformBeforeSideEffects(t *testing.T) {
	mkdirCalled := false
	runCalled := false
	deps := testmanagerdCommandDeps{
		goos: "windows",
		mkdirTemp: func(dir, pattern string) (string, error) {
			mkdirCalled = true
			return "", nil
		},
		mkdirAll: func(path string, perm os.FileMode) error {
			mkdirCalled = true
			return nil
		},
		run: func(name string, args ...string) ([]byte, error) {
			runCalled = true
			return nil, nil
		},
	}

	if _, err := pullTestmanagerdScreenrecord("device-1", "25CC55CC-C8E3-4E87-9515-3D02508B6F35", "out", deps); err == nil || !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("pull error = %v, want macOS-only error", err)
	}
	if mkdirCalled || runCalled {
		t.Fatalf("unsupported platform caused side effects: mkdir=%t run=%t", mkdirCalled, runCalled)
	}
}

func TestPullTestmanagerdScreenrecordCreatesDirectoryAndBuildsDestination(t *testing.T) {
	baseDir := t.TempDir()
	outputDir := filepath.Join(baseDir, "nested", "videos")
	videoID := "25CC55CC-C8E3-4E87-9515-3D02508B6F35"
	var commandName string
	var commandArgs []string
	var stagingPath string
	callCount := 0
	deps := testmanagerdDepsForTest(t, baseDir, func(name string, args ...string) ([]byte, error) {
		callCount++
		commandName = name
		commandArgs = append([]string(nil), args...)
		stagingPath = argumentValue(t, args, "--destination")
		if err := os.WriteFile(stagingPath, []byte("movie"), 0o644); err != nil {
			t.Fatalf("simulate copied file: %v", err)
		}
		return nil, nil
	})

	result, err := pullTestmanagerdScreenrecord("device-1", videoID, outputDir, deps)
	if err != nil {
		t.Fatalf("pullTestmanagerdScreenrecord: %v", err)
	}
	if callCount != 1 || commandName != "xcrun" {
		t.Fatalf("runner calls = %d, command = %q", callCount, commandName)
	}
	wantPath := filepath.Join(outputDir, videoID+".mov")
	if result.LocalPath != wantPath {
		t.Fatalf("local path = %q, want %q", result.LocalPath, wantPath)
	}
	wantRemote := testmanagerdAttachmentsDir + "/" + videoID
	wantArgs := pullTestmanagerdScreenrecordArgs("device-1", wantRemote, stagingPath)
	if !reflect.DeepEqual(commandArgs, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", commandArgs, wantArgs)
	}
	if filepath.Dir(filepath.Dir(stagingPath)) != outputDir {
		t.Fatalf("staging path %q is not inside output directory %q", stagingPath, outputDir)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("copied file missing: %v", err)
	}
}

func TestPullTestmanagerdScreenrecordRejectsInvalidUUID(t *testing.T) {
	runCalled := false
	deps := testmanagerdDepsForTest(t, t.TempDir(), func(name string, args ...string) ([]byte, error) {
		runCalled = true
		return nil, nil
	})
	_, err := pullTestmanagerdScreenrecord("device-1", "../../recording", t.TempDir(), deps)
	if err == nil || !strings.Contains(err.Error(), "canonical UUID") {
		t.Fatalf("error = %v, want canonical UUID error", err)
	}
	if runCalled {
		t.Fatal("runner called for invalid UUID")
	}
}

func TestPullTestmanagerdScreenrecordRefusesExistingDestination(t *testing.T) {
	outputDir := t.TempDir()
	videoID := "25CC55CC-C8E3-4E87-9515-3D02508B6F35"
	destination := filepath.Join(outputDir, videoID+".mov")
	if err := os.WriteFile(destination, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create existing destination: %v", err)
	}
	runCalled := false
	deps := testmanagerdDepsForTest(t, t.TempDir(), func(name string, args ...string) ([]byte, error) {
		runCalled = true
		return nil, nil
	})

	_, err := pullTestmanagerdScreenrecord("device-1", videoID, outputDir, deps)
	if err == nil || !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("error = %v, want destination exists error", err)
	}
	if runCalled {
		t.Fatal("runner called despite existing destination")
	}
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != "existing" {
		t.Fatalf("existing destination changed: contents=%q error=%v", contents, readErr)
	}
}

func testmanagerdDepsForTest(t *testing.T, tempBase string, runner func(name string, args ...string) ([]byte, error)) testmanagerdCommandDeps {
	t.Helper()
	return testmanagerdCommandDeps{
		goos: "darwin",
		run:  runner,
		mkdirTemp: func(dir, pattern string) (string, error) {
			if dir == "" {
				dir = tempBase
			}
			return os.MkdirTemp(dir, pattern)
		},
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

func argumentValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1]
		}
	}
	t.Fatalf("argument %s not found in %#v", flag, args)
	return ""
}
