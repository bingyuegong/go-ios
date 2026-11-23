package fileservice

import (
	"testing"
	"time"
)

func TestParseFileNodes(t *testing.T) {
	response := map[string]interface{}{
		"FileList": []interface{}{
			map[string]interface{}{
				"metadata":  map[string]interface{}{"lastModTime": int64(1795172257), "size": uint64(0x20dbf66), "permissions": int64(420)},
				"resources": uint64(0xc),
				"url": map[string]interface{}{
					"base":     map[string]interface{}{"relative": "file:///private/var/mobile/Containers/Data/InternalDaemon/X/tmp/Attachments/"},
					"relative": "C0CC5D72-2919-4D3E-8F4C-FE28567F889F",
				},
			},
			map[string]interface{}{
				"metadata": map[string]interface{}{"lastModTime": float64(1761468844.5), "size": int64(0)},
				"url":      map[string]interface{}{"relative": "1A5ACD39-994F-4D45-A792-92DEA5545948.sb-ef9f1177-dHlrBt"},
			},
		},
	}
	nodes, err := parseFileNodes(response)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes", len(nodes))
	}
	if nodes[0].Name != "C0CC5D72-2919-4D3E-8F4C-FE28567F889F" || nodes[0].Size != 34455398 {
		t.Fatalf("unexpected node: %+v", nodes[0])
	}
	if !nodes[0].LastModTime.Equal(time.Date(2026, 11, 20, 10, 57, 37, 0, time.UTC)) {
		t.Fatalf("unexpected time: %v", nodes[0].LastModTime.UTC())
	}
	if nodes[1].Size != 0 || nodes[1].LastModTime.UTC().Format(time.RFC3339) != "2025-10-26T08:54:04Z" {
		t.Fatalf("unexpected node: %+v / %s", nodes[1], nodes[1].LastModTime.UTC().Format(time.RFC3339))
	}
}

func TestParseFileNodesRejectsMalformed(t *testing.T) {
	for _, response := range []map[string]interface{}{
		{},
		{"FileList": "x"},
		{"FileList": []interface{}{"x"}},
		{"FileList": []interface{}{map[string]interface{}{"metadata": map[string]interface{}{}}}},
	} {
		if _, err := parseFileNodes(response); err == nil {
			t.Fatalf("expected error for %v", response)
		}
	}
}
