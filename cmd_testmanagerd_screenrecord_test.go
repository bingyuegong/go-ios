package main

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/fileservice"
	"github.com/google/uuid"
)

const (
	testUUIDA      = "02E0436A-C032-46E6-95F3-26967B154A68"
	testUUIDB      = "0331C26C-4487-41E4-BAC6-52D25B67BF36"
	testSandboxTmp = "1A5ACD39-994F-4D45-A792-92DEA5545948.sb-ef9f1177-dHlrBt"
)

func TestNormalizeTestmanagerdAttachmentPathsIncludesSandboxTemporaryFiles(t *testing.T) {
	targets, skipped, err := normalizeTestmanagerdAttachmentPaths([]string{
		testUUIDA,
		"./" + testSandboxTmp,
		"tmp/Attachments/" + testUUIDB,
		".",
		"Logs",
		testUUIDA + ".",
		"1A5ACD39-994F-4D45-A792-92DEA554594",
		"tmp/Attachments/sub/" + testUUIDA,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTargets := []string{"tmp/Attachments/" + testUUIDA, "tmp/Attachments/" + testSandboxTmp, "tmp/Attachments/" + testUUIDB}
	if !reflect.DeepEqual(targets, wantTargets) {
		t.Fatalf("targets = %v, want %v", targets, wantTargets)
	}
	wantSkipped := []string{"Logs", testUUIDA + ".", "1A5ACD39-994F-4D45-A792-92DEA554594", "sub/" + testUUIDA}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Fatalf("skipped = %v, want %v", skipped, wantSkipped)
	}
}

func TestNormalizeTestmanagerdAttachmentPathsRejectsOutsidePaths(t *testing.T) {
	for _, entry := range []string{"tmp/Other/" + testUUIDA, "tmp/Attachments/../" + testUUIDA, `a\b`} {
		if _, _, err := normalizeTestmanagerdAttachmentPaths([]string{entry}); err == nil {
			t.Fatalf("expected error for %q", entry)
		}
	}
}

type fakeTestmanagerdFileService struct {
	files    map[string]bool
	removed  *[]string
	stubborn map[string]int // acknowledged-but-ignored removals per name
	nodes    []fileservice.FileNode
	// removeUnsupported makes RemoveFile fail like iOS 18 / 26.7.3 (error 11014).
	removeUnsupported bool
	// sizes holds file sizes for ListDirectoryFileNodes when nodes is nil.
	sizes map[string]int64
}

func (f *fakeTestmanagerdFileService) PushFile(p string, _ io.Reader, fileSize int64, _ int64, _, _ int64) error {
	name := p[len(testmanagerdAttachmentsDir)+1:]
	f.files[name] = true
	if f.sizes != nil {
		f.sizes[name] = fileSize
	}
	return nil
}

func (f *fakeTestmanagerdFileService) ListDirectoryFileNodes(string) ([]fileservice.FileNode, error) {
	if f.nodes != nil {
		return f.nodes, nil
	}
	nodes := []fileservice.FileNode{}
	for name := range f.files {
		nodes = append(nodes, fileservice.FileNode{Name: name, Size: f.sizes[name]})
	}
	return nodes, nil
}

func (f *fakeTestmanagerdFileService) ListDirectory(string) ([]string, error) {
	var names []string
	for name := range f.files {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeTestmanagerdFileService) PullFile(string, io.Writer) error { return nil }

func (f *fakeTestmanagerdFileService) RemoveFile(p string) error {
	name := p[len(testmanagerdAttachmentsDir)+1:]
	if f.removeUnsupported {
		return fmt.Errorf("RemoveFile: device error: 11014: %w", fileservice.ErrCommandNotSupported)
	}
	*f.removed = append(*f.removed, p)
	if f.stubborn[name] > 0 {
		f.stubborn[name]--
		return nil
	}
	delete(f.files, name)
	return nil
}

func (f *fakeTestmanagerdFileService) Close() error { return nil }

func TestDeleteAllTestmanagerdScreenrecordDeletesUUIDAndSandboxFiles(t *testing.T) {
	files := map[string]bool{testUUIDA: true, testUUIDB: true, testSandboxTmp: true, "Logs": true}
	var removed []string
	deps := defaultTestmanagerdCommandDeps()
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		return &fakeTestmanagerdFileService{files: files, removed: &removed}, nil
	}
	device := ios.DeviceEntry{}
	device.Properties.SerialNumber = "test-udid"

	result, err := deleteAllTestmanagerdScreenrecordNative(device, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DeletedCount != 3 || len(removed) != 3 {
		t.Fatalf("deleted %d (%v), want 3", result.DeletedCount, removed)
	}
	if !files["Logs"] || len(files) != 1 {
		t.Fatalf("only the non-recording entry must remain, remaining = %v", files)
	}
	if !reflect.DeepEqual(result.Skipped, []string{"Logs"}) {
		t.Fatalf("skipped = %v", result.Skipped)
	}
}

func newFakeDeps(files map[string]bool, stubborn map[string]int, removed *[]string, onList func()) testmanagerdCommandDeps {
	deps := defaultTestmanagerdCommandDeps()
	deps.sleep = func(time.Duration) {}
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		if onList != nil {
			onList()
		}
		return &fakeTestmanagerdFileService{files: files, removed: removed, stubborn: stubborn}, nil
	}
	return deps
}

func testDevice() ios.DeviceEntry {
	device := ios.DeviceEntry{}
	device.Properties.SerialNumber = "test-udid"
	return device
}

func TestDeleteAllTestmanagerdScreenrecordRetriesAcknowledgedButRemaining(t *testing.T) {
	files := map[string]bool{testUUIDA: true, testUUIDB: true}
	var removed []string
	deps := newFakeDeps(files, map[string]int{testUUIDA: 1}, &removed, nil)

	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DeletedCount != 2 || len(files) != 0 || len(removed) != 3 {
		t.Fatalf("deleted=%d remaining=%v requests=%v", result.DeletedCount, files, removed)
	}
}

func TestDeleteAllTestmanagerdScreenrecordReportsAllRemainingAfterRetries(t *testing.T) {
	files := map[string]bool{testUUIDA: true, testUUIDB: true}
	var removed []string
	deps := newFakeDeps(files, map[string]int{testUUIDA: 99}, &removed, nil)

	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), deps)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "1 testmanagerd") || !strings.Contains(err.Error(), testUUIDA) {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DeletedCount != 1 || len(removed) != 1+testmanagerdDeleteMaxRounds {
		t.Fatalf("deleted=%d requests=%d", result.DeletedCount, len(removed))
	}
}

func TestDeleteAllTestmanagerdScreenrecordDeletesRecordingsCreatedDuringCleanup(t *testing.T) {
	files := map[string]bool{testUUIDA: true}
	var removed []string
	calls := 0
	// The 2nd session is the deletion session; a new recording appears then.
	deps := newFakeDeps(files, nil, &removed, func() {
		calls++
		if calls == 2 {
			files[testUUIDB] = true
		}
	})

	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 || result.DeletedCount != 2 {
		t.Fatalf("new recording must also be deleted: files=%v deleted=%d", files, result.DeletedCount)
	}
}

type fakeAttachmentDeleter struct {
	files    map[string]bool
	requests *[][]uuid.UUID
	// unregistered names are unknown to testmanagerd and are never deleted.
	unregistered map[string]bool
}

func (f *fakeAttachmentDeleter) DeleteAttachments(_ context.Context, ids []uuid.UUID) error {
	*f.requests = append(*f.requests, ids)
	for _, id := range ids {
		name := strings.ToUpper(id.String())
		if !f.unregistered[name] {
			delete(f.files, name)
		}
	}
	return nil
}

func (f *fakeAttachmentDeleter) Close() error { return nil }

func iOS18Deps(files map[string]bool, requests *[][]uuid.UUID) testmanagerdCommandDeps {
	return noRemoveFileDeps(files, map[string]int64{}, nil, requests)
}

// noRemoveFileDeps models a device whose file service rejects RemoveFile.
func noRemoveFileDeps(files map[string]bool, sizes map[string]int64, unregistered map[string]bool, requests *[][]uuid.UUID) testmanagerdCommandDeps {
	var removed []string
	deps := newFakeDeps(files, nil, &removed, nil)
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		return &fakeTestmanagerdFileService{files: files, removed: &removed, removeUnsupported: true, sizes: sizes}, nil
	}
	deps.newAttachmentDeleter = func(ios.DeviceEntry) (testmanagerdAttachmentDeleter, error) {
		return &fakeAttachmentDeleter{files: files, requests: requests, unregistered: unregistered}, nil
	}
	return deps
}

func TestDeleteAllFallsBackToTestmanagerdWhenRemoveFileUnsupported(t *testing.T) {
	files := map[string]bool{}
	for i := 0; i < 2*testmanagerdDeleteBatchSize+10; i++ {
		files[strings.ToUpper(uuid.NewString())] = true
	}
	var requests [][]uuid.UUID
	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), iOS18Deps(files, &requests))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 || result.DeletedCount != 2*testmanagerdDeleteBatchSize+10 || result.Transport != "testmanagerd" {
		t.Fatalf("remaining=%d deleted=%d transport=%s", len(files), result.DeletedCount, result.Transport)
	}
	if len(requests) != 3 || len(requests[0]) != testmanagerdDeleteBatchSize || len(requests[2]) != 10 {
		t.Fatalf("unexpected batching: %d requests", len(requests))
	}
}

func TestDeleteAllTruncatesSandboxLeftoverWhenRemoveFileUnsupported(t *testing.T) {
	// Models the iOS 26.7.3 device: one 14.2 GB UUID.sb-* leftover plus a
	// registered recording.
	files := map[string]bool{testUUIDA: true, testSandboxTmp: true}
	sizes := map[string]int64{testUUIDA: 100, testSandboxTmp: 15294815039}
	var requests [][]uuid.UUID
	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), noRemoveFileDeps(files, sizes, nil, &requests))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if files[testUUIDA] || !files[testSandboxTmp] || sizes[testSandboxTmp] != 0 {
		t.Fatalf("files=%v sizes=%v", files, sizes)
	}
	if !reflect.DeepEqual(result.Truncated, []string{testSandboxTmp}) || result.DeletedCount != 1 || result.Transport != "testmanagerd" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(requests) != 1 {
		t.Fatalf("must not retry rounds that cannot make progress; requests=%d", len(requests))
	}
}

func TestDeleteAllTruncatesUnregisteredUUIDWithoutRetrying(t *testing.T) {
	files := map[string]bool{testUUIDB: true}
	sizes := map[string]int64{testUUIDB: 4096}
	var requests [][]uuid.UUID
	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), noRemoveFileDeps(files, sizes, map[string]bool{testUUIDB: true}, &requests))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sizes[testUUIDB] != 0 || !reflect.DeepEqual(result.Truncated, []string{testUUIDB}) || result.DeletedCount != 0 {
		t.Fatalf("sizes=%v result=%+v", sizes, result)
	}
	if len(requests) != 1 {
		t.Fatalf("no-progress testmanagerd rounds must stop after one attempt; requests=%d", len(requests))
	}
}

func TestDeleteAllSandboxLeftoverIsRemovedWhenRemoveFileSupported(t *testing.T) {
	files := map[string]bool{testSandboxTmp: true}
	var removed []string
	result, err := deleteAllTestmanagerdScreenrecordNative(testDevice(), newFakeDeps(files, nil, &removed, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 || result.DeletedCount != 1 || len(result.Truncated) != 0 || result.Transport != "fileservice" {
		t.Fatalf("files=%v result=%+v", files, result)
	}
}
