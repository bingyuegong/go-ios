package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/bingyuegong/go-ios/ios"
)

// testmanagerdAttachmentEntry is one entry of
// com.apple.testmanagerd:tmp/Attachments as printed by screenrecord_ls -J.
type testmanagerdAttachmentEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Time string `json:"time"`
}

const testmanagerdListTimeLayout = "2006-01-02 15:04:05"

// runListTestmanagerdScreenrecordCommand lists tmp/Attachments with the native
// CoreDevice fileservice, so macOS, Windows and Linux behave identically.
// Like `ios list`, the default is a table and -J prints JSON.
func runListTestmanagerdScreenrecordCommand(ctx commandContext) {
	if !ctx.Device.SupportsRsd() {
		exitIfError("screenrecord_ls", fmt.Errorf("native CoreDevice fileservice requires iOS 17+ with an active tunnel"))
	}
	entries, err := listTestmanagerdScreenrecordNative(ctx.Device, defaultTestmanagerdCommandDeps())
	exitIfError("screenrecord_ls", err)

	if jsonOutput, _ := ctx.Args.Bool("-J"); jsonOutput {
		fmt.Println(convertToJSONString(entries))
		return
	}
	printTestmanagerdScreenrecordTable(os.Stdout, entries)
}

func listTestmanagerdScreenrecordNative(device ios.DeviceEntry, deps testmanagerdCommandDeps) ([]testmanagerdAttachmentEntry, error) {
	if deps.newFileService == nil {
		return nil, fmt.Errorf("fileservice factory is unavailable")
	}
	conn, err := deps.newFileService(device)
	if err != nil {
		return nil, fmt.Errorf("connect to native CoreDevice fileservice: %w", err)
	}
	nodes, listErr := conn.ListDirectoryFileNodes(testmanagerdAttachmentsDir)
	_ = conn.Close()
	if listErr != nil {
		return nil, fmt.Errorf("list %s: %w", testmanagerdAttachmentsDir, listErr)
	}

	sort.SliceStable(nodes, func(i, j int) bool {
		if !nodes[i].LastModTime.Equal(nodes[j].LastModTime) {
			return nodes[i].LastModTime.Before(nodes[j].LastModTime)
		}
		return nodes[i].Name < nodes[j].Name
	})
	entries := make([]testmanagerdAttachmentEntry, 0, len(nodes))
	for _, node := range nodes {
		entries = append(entries, testmanagerdAttachmentEntry{
			Name: node.Name,
			Size: node.Size,
			Time: formatTestmanagerdListTime(node.LastModTime),
		})
	}
	return entries, nil
}

func formatTestmanagerdListTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(testmanagerdListTimeLayout)
}

func printTestmanagerdScreenrecordTable(out io.Writer, entries []testmanagerdAttachmentEntry) {
	nameWidth := len("NAME")
	for _, entry := range entries {
		if len(entry.Name) > nameWidth {
			nameWidth = len(entry.Name)
		}
	}
	fmt.Fprintf(out, "%-*s  %10s  %s\n", nameWidth, "NAME", "SIZE", "TIME")
	for _, entry := range entries {
		fmt.Fprintf(out, "%-*s  %10s  %s\n", nameWidth, entry.Name, formatTestmanagerdSize(entry.Size), entry.Time)
	}
}

func formatTestmanagerdSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	suffixes := []string{"KB", "MB", "GB", "TB"}
	index := -1
	for value >= unit && index < len(suffixes)-1 {
		value /= unit
		index++
	}
	return fmt.Sprintf("%.1f %s", value, suffixes[index])
}
