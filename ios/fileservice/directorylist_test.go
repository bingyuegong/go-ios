package fileservice

import (
	"fmt"
	"testing"
	"time"
)

func batchOf(start, count int, uuid string) map[string]interface{} {
	list := make([]interface{}, 0, count)
	for i := 0; i < count; i++ {
		list = append(list, fmt.Sprintf("entry-%04d", start+i))
	}
	return map[string]interface{}{"MessageUUID": uuid, "FileList": list}
}

// scriptedReceiver returns the scripted replies in order, then blocks forever
// (like a device that sends nothing more).
func scriptedReceiver(replies ...map[string]interface{}) (func() (map[string]interface{}, error), *int) {
	calls := 0
	return func() (map[string]interface{}, error) {
		calls++
		if calls <= len(replies) {
			return replies[calls-1], nil
		}
		select {}
	}, &calls
}

func TestReceiveDirectoryListEntriesSingleShortBatch(t *testing.T) {
	receive, calls := scriptedReceiver(batchOf(0, 59, "u"))
	entries, pending, err := receiveDirectoryListEntries(receive, "u", time.Second)
	if err != nil || pending || len(entries) != 59 || *calls != 1 {
		t.Fatalf("entries=%d pending=%v calls=%d err=%v", len(entries), pending, *calls, err)
	}
}

func TestReceiveDirectoryListEntriesMultipleBatches(t *testing.T) {
	// 769 entries = 6 full batches + 1, as observed on an iPhone 11 / iOS 26.6.1.
	replies := []map[string]interface{}{}
	for i := 0; i < 6; i++ {
		replies = append(replies, batchOf(i*128, 128, "ABC"))
	}
	replies = append(replies, batchOf(768, 1, "ABC"))
	receive, _ := scriptedReceiver(replies...)
	entries, pending, err := receiveDirectoryListEntries(receive, "abc", time.Second)
	if err != nil || pending || len(entries) != 769 {
		t.Fatalf("entries=%d pending=%v err=%v", len(entries), pending, err)
	}
	if entries[0] != "entry-0000" || entries[768] != "entry-0768" {
		t.Fatalf("unexpected order: first=%v last=%v", entries[0], entries[768])
	}
}

func TestReceiveDirectoryListEntriesEmptyTerminator(t *testing.T) {
	receive, _ := scriptedReceiver(batchOf(0, 128, "u"), batchOf(128, 128, "u"), batchOf(0, 0, "u"))
	entries, pending, err := receiveDirectoryListEntries(receive, "u", time.Second)
	if err != nil || pending || len(entries) != 256 {
		t.Fatalf("entries=%d pending=%v err=%v", len(entries), pending, err)
	}
}

func TestReceiveDirectoryListEntriesStallIsAnError(t *testing.T) {
	receive, _ := scriptedReceiver(batchOf(0, 128, "u"), batchOf(128, 128, "u"))
	started := time.Now()
	entries, pending, err := receiveDirectoryListEntries(receive, "u", 50*time.Millisecond)
	if err == nil || !pending || entries != nil {
		t.Fatalf("a stalled listing must be an error, not a truncated result: entries=%d pending=%v err=%v", len(entries), pending, err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("continuation wait was not bounded")
	}
}

func TestReceiveDirectoryListEntriesErrors(t *testing.T) {
	receive, _ := scriptedReceiver(batchOf(0, 128, "u"), batchOf(0, 1, "other"))
	if _, _, err := receiveDirectoryListEntries(receive, "u", time.Second); err == nil {
		t.Fatal("expected MessageUUID mismatch error")
	}
	failing := func() (map[string]interface{}, error) { return nil, fmt.Errorf("boom") }
	if _, _, err := receiveDirectoryListEntries(failing, "u", time.Second); err == nil {
		t.Fatal("expected receive error")
	}
	receive, _ = scriptedReceiver(map[string]interface{}{"MessageUUID": "u"})
	if _, _, err := receiveDirectoryListEntries(receive, "u", time.Second); err == nil {
		t.Fatal("expected missing FileList error")
	}
}

func TestConnectionRefusesReuseAfterPendingListing(t *testing.T) {
	c := &Connection{streamPending: true}
	if _, err := c.ListDirectory("tmp"); err == nil {
		t.Fatal("ListDirectory must refuse a connection with a pending listing")
	}
	if err := c.RemoveFile("tmp/x"); err == nil {
		t.Fatal("RemoveFile must refuse a connection with a pending listing")
	}
	if _, err := c.ListDirectoryFileNodes("tmp"); err == nil {
		t.Fatal("ListDirectoryFileNodes must refuse a connection with a pending listing")
	}
}
