package fileservice

import (
	"fmt"
	"strings"
	"time"
)

const (
	// directoryListBatchSize is the number of entries the device puts into one
	// RetrieveDirectoryList / ListDirectoryFileNodes reply. Larger directories
	// are streamed as several replies with the same MessageUUID; the final reply
	// carries fewer entries than a full batch, or is empty when the entry count
	// is an exact multiple of the batch size (observed on iOS 26.6.1).
	directoryListBatchSize = 128
	// directoryListContinuationTimeout bounds the wait for a follow-up batch
	// after a full batch. A stall is reported as an error rather than treated
	// as the end of the listing, so callers never act on a truncated listing.
	directoryListContinuationTimeout = 15 * time.Second
)

type directoryListReceiveResult struct {
	message map[string]interface{}
	err     error
}

// receiveDirectoryListEntries collects the FileList entries of every reply
// that belongs to one directory-listing request. pending reports that a
// receive is still outstanding on the stream, so the connection must not be
// reused for further requests; it is only set together with an error.
func receiveDirectoryListEntries(receive func() (map[string]interface{}, error), messageUUID string, timeout time.Duration) (entries []interface{}, pending bool, err error) {
	first, err := receive()
	if err != nil {
		return nil, false, fmt.Errorf("failed to receive response: %w", err)
	}
	batch, err := directoryListBatch(first, messageUUID)
	if err != nil {
		return nil, false, err
	}
	entries = append(entries, batch...)
	fullBatch := len(batch)
	if fullBatch < directoryListBatchSize {
		return entries, false, nil
	}

	for {
		results := make(chan directoryListReceiveResult, 1)
		go func() {
			message, err := receive()
			results <- directoryListReceiveResult{message: message, err: err}
		}()

		select {
		case result := <-results:
			if result.err != nil {
				return nil, false, fmt.Errorf("failed to receive directory list continuation after %d entries: %w", len(entries), result.err)
			}
			batch, err := directoryListBatch(result.message, messageUUID)
			if err != nil {
				return nil, false, err
			}
			entries = append(entries, batch...)
			if len(batch) < fullBatch {
				return entries, false, nil
			}
		case <-time.After(timeout):
			return nil, true, fmt.Errorf("directory listing incomplete: no further reply within %s after %d entries", timeout, len(entries))
		}
	}
}

func directoryListBatch(response map[string]interface{}, messageUUID string) ([]interface{}, error) {
	if err := extractError(response); err != nil {
		return nil, err
	}
	if replyUUID, ok := response["MessageUUID"].(string); ok && messageUUID != "" && !strings.EqualFold(replyUUID, messageUUID) {
		return nil, fmt.Errorf("reply MessageUUID %s does not match request %s", replyUUID, messageUUID)
	}
	rawList, ok := response["FileList"]
	if !ok {
		return nil, fmt.Errorf("missing FileList in response")
	}
	list, ok := rawList.([]interface{})
	if !ok {
		return nil, fmt.Errorf("FileList is not an array")
	}
	return list, nil
}
