package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/docopt/docopt-go"
)

type screencordRoundTripFunc func(*http.Request) (*http.Response, error)

func (f screencordRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestExecuteScreencordCommandUsesFixedPostEndpoints(t *testing.T) {
	for action, expectedPath := range screencordPaths {
		t.Run(action, func(t *testing.T) {
			client := &http.Client{Transport: screencordRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost {
					t.Fatalf("method = %q, want POST", request.Method)
				}
				if request.URL.Path != expectedPath {
					t.Fatalf("path = %q, want %q", request.URL.Path, expectedPath)
				}
				if request.Header.Get("Accept") != "application/json" {
					t.Fatalf("Accept = %q", request.Header.Get("Accept"))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"value":{"ok":true}}`)),
					Header:     make(http.Header),
				}, nil
			})}
			device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: "device-1"}}

			result, err := executeScreencordCommand(context.Background(), client, "http://wda.test:8100", device, action, "test")
			if err != nil {
				t.Fatalf("executeScreencordCommand: %v", err)
			}
			if result.UDID != "device-1" || result.Action != action || result.Path != expectedPath || result.StatusCode != http.StatusOK {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestExecuteScreencordCommandRejectsHTTPError(t *testing.T) {
	client := &http.Client{Transport: screencordRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       io.NopCloser(strings.NewReader(`{"value":{"error":"already recording"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := executeScreencordCommand(context.Background(), client, "http://wda.test:8100", ios.DeviceEntry{}, "start", "test")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "already recording") {
		t.Fatalf("error = %v, want HTTP status and WDA response", err)
	}
}

func TestExecuteScreencordCommandRejectsUnknownActionBeforeRequest(t *testing.T) {
	client := &http.Client{Transport: screencordRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP request must not be sent")
		return nil, nil
	})}

	_, err := executeScreencordCommand(context.Background(), client, "http://wda.test:8100", ios.DeviceEntry{}, "unknown", "test")
	if err == nil || !strings.Contains(err.Error(), "unsupported screen recording action") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewScreencordHTTPClientDefaultsToUsbmux(t *testing.T) {
	client, transportName, cleanup, err := newScreencordHTTPClient(ios.DeviceEntry{DeviceID: 42}, "")
	if err != nil {
		t.Fatalf("newScreencordHTTPClient: %v", err)
	}
	defer cleanup()
	if transportName != "usbmux" {
		t.Fatalf("transport = %q, want usbmux", transportName)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("usbmux HTTP transport must not use an HTTP proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("usbmux HTTP transport is missing DialContext")
	}
}

func TestNewScreencordHTTPClientUsesExplicitURL(t *testing.T) {
	client, transportName, cleanup, err := newScreencordHTTPClient(ios.DeviceEntry{}, "http://127.0.0.1:18100")
	if err != nil {
		t.Fatalf("newScreencordHTTPClient: %v", err)
	}
	defer cleanup()
	if client == nil || transportName != "url" {
		t.Fatalf("client = %v, transport = %q", client, transportName)
	}

	if _, _, _, err := newScreencordHTTPClient(ios.DeviceEntry{}, "not-a-url"); err == nil {
		t.Fatal("invalid URL was accepted")
	}
}

func TestScreencordHTTPClientRejectsRedirectWithoutReplayingPost(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.Method != http.MethodPost || request.URL.Path != screencordPaths["start"] {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		writer.Header().Set("Location", "/redirected")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client, transport, cleanup, err := newScreencordHTTPClient(ios.DeviceEntry{}, server.URL)
	if err != nil {
		t.Fatalf("newScreencordHTTPClient: %v", err)
	}
	defer cleanup()
	_, err = executeScreencordCommand(context.Background(), client, server.URL, ios.DeviceEntry{}, "start", transport)
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("error = %v, want redirect rejection", err)
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want exactly one POST", requestCount)
	}
}

func TestScreencordExplicitWDAURLPrecedence(t *testing.T) {
	t.Setenv("GO_IOS_WDA_URL", "http://env.example:8100")
	if got := screencordExplicitWDAURL(docopt.Opts{"--wda-url": "http://flag.example:8100"}); got != "http://flag.example:8100" {
		t.Fatalf("flag URL = %q", got)
	}
	if got := screencordExplicitWDAURL(docopt.Opts{}); got != "http://env.example:8100" {
		t.Fatalf("environment URL = %q", got)
	}
}

func TestScreencordUsesDirectUsbmuxOnlyWithoutExplicitURL(t *testing.T) {
	t.Setenv("GO_IOS_WDA_URL", "")
	if !screencordUsesDirectUsbmux(docopt.Opts{"screenrecord_start": true}) {
		t.Fatal("screenrecord_start should use direct usbmux by default")
	}
	if screencordUsesDirectUsbmux(docopt.Opts{"screenrecord_start": true, "--wda-url": "http://127.0.0.1:18100"}) {
		t.Fatal("explicit WDA URL should disable direct usbmux mode")
	}
	if screencordUsesDirectUsbmux(docopt.Opts{"apps": true}) {
		t.Fatal("unrelated command was classified as direct screencord usbmux")
	}
}
