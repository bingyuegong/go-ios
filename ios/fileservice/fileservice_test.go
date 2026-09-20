package fileservice

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildFileSystemOperationRequest(t *testing.T) {
	for _, operationType := range []string{"RemoveFile", "RemoveDirectory"} {
		t.Run(operationType, func(t *testing.T) {
			got, err := buildFileSystemOperationRequest("session-1", operationType, "tmp/Attachments/video")
			if err != nil {
				t.Fatalf("buildFileSystemOperationRequest: %v", err)
			}
			want := map[string]interface{}{
				"Cmd":           "FileSystemOperation",
				"SessionID":     "session-1",
				"OperationType": operationType,
				"Path":          "tmp/Attachments/video",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("request = %#v, want %#v", got, want)
			}
		})
	}
}

func TestValidateFileSystemOperationResponse(t *testing.T) {
	if err := validateFileSystemOperationResponse(map[string]interface{}{"Response": uint64(1)}); err != nil {
		t.Fatalf("success response returned error: %v", err)
	}
	for name, response := range map[string]map[string]interface{}{
		"missing response": {},
		"failure response": {"Response": uint64(0)},
		"encoded error":    {"EncodedError": map[string]interface{}{"LocalizedDescription": "denied"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFileSystemOperationResponse(response); err == nil {
				t.Fatalf("response %#v unexpectedly succeeded", response)
			}
		})
	}
}

func TestBuildFileSystemOperationRequestPreservesPathWhitespace(t *testing.T) {
	got, err := buildFileSystemOperationRequest("session-1", "RemoveFile", "tmp/file ")
	if err != nil {
		t.Fatalf("buildFileSystemOperationRequest: %v", err)
	}
	if got["Path"] != "tmp/file " {
		t.Fatalf("path = %q, want exact original path", got["Path"])
	}
}

func TestBuildFileSystemOperationRequestRejectsInvalidInput(t *testing.T) {
	for name, testCase := range map[string]struct {
		sessionID     string
		operationType string
		path          string
		wantError     string
	}{
		"missing session": {operationType: "RemoveFile", path: "tmp/file", wantError: "session ID is required"},
		"missing path":    {sessionID: "session-1", operationType: "RemoveFile", wantError: "path is required"},
		"unknown operation": {
			sessionID:     "session-1",
			operationType: "RemoveAll",
			path:          "tmp",
			wantError:     "unsupported operation type",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildFileSystemOperationRequest(testCase.sessionID, testCase.operationType, testCase.path)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantError)
			}
		})
	}
}
