package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/fileservice"
)

func TestListTestmanagerdScreenrecordNative(t *testing.T) {
	newer := time.Date(2026, 11, 20, 10, 57, 37, 0, time.UTC)
	older := time.Date(2026, 10, 26, 8, 54, 4, 0, time.UTC)
	deps := defaultTestmanagerdCommandDeps()
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		return &fakeTestmanagerdFileService{nodes: []fileservice.FileNode{
			{Name: testUUIDB, Size: 34455398, LastModTime: newer},
			{Name: testSandboxTmp, Size: 0, LastModTime: older},
		}}, nil
	}

	entries, err := listTestmanagerdScreenrecordNative(testDevice(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []testmanagerdAttachmentEntry{
		{Name: testSandboxTmp, Size: 0, Time: older.Local().Format(testmanagerdListTimeLayout)},
		{Name: testUUIDB, Size: 34455398, Time: newer.Local().Format(testmanagerdListTimeLayout)},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %+v\nwant %+v", entries, want)
	}

	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, item := range decoded {
		if len(item) != 3 || item["name"] == nil || item["size"] == nil || item["time"] == nil {
			t.Fatalf("JSON item must contain exactly name/size/time: %v", item)
		}
	}
}

func TestListTestmanagerdScreenrecordNativeEmptyIsJSONArray(t *testing.T) {
	deps := defaultTestmanagerdCommandDeps()
	deps.newFileService = func(ios.DeviceEntry) (testmanagerdFileService, error) {
		return &fakeTestmanagerdFileService{nodes: []fileservice.FileNode{}}, nil
	}
	entries, err := listTestmanagerdScreenrecordNative(testDevice(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := convertToJSONString(entries); got != "[]" {
		t.Fatalf("empty listing JSON = %q, want []", got)
	}
}

func TestPrintTestmanagerdScreenrecordTable(t *testing.T) {
	var out bytes.Buffer
	printTestmanagerdScreenrecordTable(&out, []testmanagerdAttachmentEntry{
		{Name: testUUIDB, Size: 34455398, Time: "2026-11-20 18:57:37"},
	})
	want := "NAME                                        SIZE  TIME\n" +
		testUUIDB + "     32.9 MB  2026-11-20 18:57:37\n"
	if out.String() != want {
		t.Fatalf("table =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestFormatTestmanagerdSize(t *testing.T) {
	for size, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 1367396257: "1.3 GB"} {
		if got := formatTestmanagerdSize(size); got != want {
			t.Fatalf("formatTestmanagerdSize(%d) = %q, want %q", size, got, want)
		}
	}
}
