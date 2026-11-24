package fileservice

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/bingyuegong/go-ios/ios/xpc"
	"github.com/google/uuid"
)

// FileNode is one directory entry with metadata, as returned by the
// CoreDevice ListDirectoryFileNodes command (the request devicectl uses for
// `device info files`).
type FileNode struct {
	Name        string
	Size        int64
	LastModTime time.Time
}

// ListDirectoryFileNodes lists the entries of a directory (relative to the
// domain root) together with their size and modification time.
func (c *Connection) ListDirectoryFileNodes(dirPath string) ([]FileNode, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkUsable("ListDirectoryFileNodes"); err != nil {
		return nil, err
	}

	messageUUID := uuid.New().String()
	request := map[string]interface{}{
		"Cmd":         "ListDirectoryFileNodes",
		"MessageUUID": messageUUID,
		"Path":        dirPath,
		"SessionID":   c.sessionID,
	}
	if err := c.conn.Send(request, xpc.HeartbeatRequestFlag); err != nil {
		return nil, fmt.Errorf("ListDirectoryFileNodes: failed to send request: %w", err)
	}
	list, pending, err := receiveDirectoryListEntries(c.conn.ReceiveOnClientServerStream, messageUUID, directoryListContinuationTimeout)
	c.streamPending = c.streamPending || pending
	if err != nil {
		c.streamPending = true
		return nil, fmt.Errorf("ListDirectoryFileNodes: %w", err)
	}
	nodes, err := parseFileNodes(map[string]interface{}{"FileList": list})
	if err != nil {
		return nil, fmt.Errorf("ListDirectoryFileNodes: %w", err)
	}
	return nodes, nil
}

// parseFileNodes decodes a ListDirectoryFileNodes reply of the form
// {"FileList": [{"url": {"relative": "<name>", ...}, "metadata": {"size": N, "lastModTime": <unix seconds>, ...}}]}.
func parseFileNodes(response map[string]interface{}) ([]FileNode, error) {
	rawList, ok := response["FileList"]
	if !ok {
		return nil, fmt.Errorf("missing FileList in response")
	}
	list, ok := rawList.([]interface{})
	if !ok {
		return nil, fmt.Errorf("FileList is not an array")
	}
	nodes := make([]FileNode, 0, len(list))
	for index, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("FileList[%d] is not a dictionary", index)
		}
		node := FileNode{}
		if url, ok := entry["url"].(map[string]interface{}); ok {
			node.Name, _ = url["relative"].(string)
		}
		node.Name = strings.TrimSuffix(node.Name, "/")
		if strings.Contains(node.Name, "/") {
			node.Name = path.Base(node.Name)
		}
		if node.Name == "" {
			return nil, fmt.Errorf("FileList[%d] has no url.relative name", index)
		}
		if metadata, ok := entry["metadata"].(map[string]interface{}); ok {
			if size, ok := xpcInteger(metadata["size"]); ok {
				node.Size = size
			}
			if seconds, ok := xpcInteger(metadata["lastModTime"]); ok {
				node.LastModTime = time.Unix(seconds, 0)
			} else if seconds, ok := metadata["lastModTime"].(float64); ok {
				node.LastModTime = time.Unix(0, int64(seconds*float64(time.Second)))
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func xpcInteger(value interface{}) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case uint64:
		return int64(number), true
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case uint32:
		return int64(number), true
	default:
		return 0, false
	}
}
