package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/fileservice"
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
}

func (f *fakeTestmanagerdFileService) ListDirectoryFileNodes(string) ([]fileservice.FileNode, error) {
	return f.nodes, nil
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
