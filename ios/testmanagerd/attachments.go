package testmanagerd

import (
	"context"
	"fmt"

	"github.com/bingyuegong/go-ios/ios"
	dtx "github.com/bingyuegong/go-ios/ios/dtx_codec"
	"github.com/bingyuegong/go-ios/ios/nskeyedarchiver"
	"github.com/google/uuid"
)

// AttachmentDeleter is a testmanagerd daemon control session that can delete
// XCTest attachments (for example screen recordings in
// com.apple.testmanagerd:tmp/Attachments) by UUID. It uses the same
// _IDE_deleteAttachmentsWithUUIDs: request as Xcode, which works on iOS
// versions whose CoreDevice file service has no RemoveFile operation.
type AttachmentDeleter struct {
	conn  *dtx.Connection
	proxy dtxproxy
}

// NewAttachmentDeleter opens a testmanagerd control session on an iOS 17+
// device reachable through a tunnel.
func NewAttachmentDeleter(device ios.DeviceEntry) (*AttachmentDeleter, error) {
	conn, err := dtx.NewTunnelConnection(device, testmanagerdiOS17)
	if err != nil {
		return nil, fmt.Errorf("NewAttachmentDeleter: cannot connect to testmanagerd: %w", err)
	}
	proxy := newDtxProxy(conn)
	if _, err := proxy.daemonConnection.initiateControlSessionWithCapabilities(nskeyedarchiver.XCTCapabilities{CapabilitiesDictionary: map[string]interface{}{}}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("NewAttachmentDeleter: cannot initiate control session: %w", err)
	}
	return &AttachmentDeleter{conn: conn, proxy: proxy}, nil
}

// DeleteAttachments asks testmanagerd to delete the attachments with the given
// UUIDs. The daemon acknowledges the request; callers must verify the result
// independently.
func (a *AttachmentDeleter) DeleteAttachments(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	archived := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		archived = append(archived, nskeyedarchiver.NewNSUUID(id))
	}
	reply, err := a.proxy.IDEDaemonProxy.MethodCallWithContext(ctx, "_IDE_deleteAttachmentsWithUUIDs:", archived)
	if err != nil {
		return fmt.Errorf("_IDE_deleteAttachmentsWithUUIDs: %w", err)
	}
	for _, value := range reply.Payload {
		if errValue, ok := value.(error); ok {
			return fmt.Errorf("_IDE_deleteAttachmentsWithUUIDs: device error: %w", errValue)
		}
		if dict, ok := value.(map[string]interface{}); ok {
			if _, isError := dict["NSLocalizedDescription"]; isError {
				return fmt.Errorf("_IDE_deleteAttachmentsWithUUIDs: device error: %v", dict)
			}
		}
	}
	return nil
}

// Close closes the testmanagerd connection.
func (a *AttachmentDeleter) Close() error {
	if a == nil || a.conn == nil {
		return nil
	}
	return a.conn.Close()
}
