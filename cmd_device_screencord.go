package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/docopt/docopt-go"
)

const (
	screencordWDADevicePort  = uint16(8100)
	screencordRequestTimeout = 60 * time.Second
)

var screencordPaths = map[string]string{
	"start":  "/wda/screenrecord/start",
	"stop":   "/wda/screenrecord/stop",
	"status": "/wda/screenrecord/status",
}

type screencordCommandResult struct {
	UDID       string
	Action     string
	Path       string
	Transport  string
	StatusCode int
	Body       []byte
}

type screencordHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func runScreencordStartCommand(ctx commandContext) {
	runScreencordCommand(ctx, "start")
}

func runScreencordStopCommand(ctx commandContext) {
	runScreencordCommand(ctx, "stop")
}

func runScreencordStatusCommand(ctx commandContext) {
	runScreencordCommand(ctx, "status")
}

func runScreencordCommand(commandCtx commandContext, action string) {
	baseURL := screencordExplicitWDAURL(commandCtx.Args)
	client, transport, cleanup, err := newScreencordHTTPClient(commandCtx.Device, baseURL)
	exitIfError("create WDA screen recording client", err)
	defer cleanup()

	requestCtx, cancel := context.WithTimeout(context.Background(), screencordRequestTimeout)
	defer cancel()
	result, err := executeScreencordCommand(requestCtx, client, baseURL, commandCtx.Device, action, transport)
	exitIfError("WDA screen recording "+action, err)
	printScreencordResult(result)
}

func newScreencordHTTPClient(device ios.DeviceEntry, explicitURL string) (*http.Client, string, func(), error) {
	if explicitURL != "" {
		parsed, err := url.ParseRequestURI(explicitURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, "", func() {}, fmt.Errorf("invalid WDA URL %q", explicitURL)
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		client := &http.Client{
			Transport:     transport,
			Timeout:       screencordRequestTimeout,
			CheckRedirect: rejectScreencordRedirect,
		}
		return client, "url", transport.CloseIdleConnections, nil
	}

	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return ios.DialDevicePortContext(ctx, device, screencordWDADevicePort)
		},
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       screencordRequestTimeout,
		CheckRedirect: rejectScreencordRedirect,
	}
	return client, "usbmux", transport.CloseIdleConnections, nil
}

func rejectScreencordRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func executeScreencordCommand(ctx context.Context, client screencordHTTPDoer, baseURL string, device ios.DeviceEntry, action, transport string) (screencordCommandResult, error) {
	result := screencordCommandResult{
		UDID:      device.Properties.SerialNumber,
		Action:    action,
		Transport: transport,
	}
	endpoint, ok := screencordPaths[action]
	if !ok {
		return result, fmt.Errorf("unsupported screen recording action %q", action)
	}
	result.Path = endpoint
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultWDAURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+endpoint, http.NoBody)
	if err != nil {
		return result, fmt.Errorf("create WDA request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return result, fmt.Errorf("POST %s through %s failed: %w", endpoint, transport, err)
	}
	defer func() { _ = response.Body.Close() }()
	result.StatusCode = response.StatusCode
	result.Body, err = io.ReadAll(response.Body)
	if err != nil {
		return result, fmt.Errorf("read WDA response for %s: %w", endpoint, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("WDA returned HTTP %d for %s: %s", response.StatusCode, endpoint, strings.TrimSpace(string(result.Body)))
	}
	return result, nil
}

func screencordExplicitWDAURL(args docopt.Opts) string {
	for _, key := range []string{"--wda-url", "-wda-url"} {
		value, _ := args.String(key)
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(os.Getenv("GO_IOS_WDA_URL"))
}

func screencordUsesDirectUsbmux(args docopt.Opts) bool {
	isScreencordCommand := boolArg(args, "screenrecord_start") || boolArg(args, "screenrecord_stop") || boolArg(args, "screenrecord_status")
	return isScreencordCommand && screencordExplicitWDAURL(args) == ""
}

func printScreencordResult(result screencordCommandResult) {
	if len(result.Body) != 0 {
		var response interface{}
		if err := json.Unmarshal(result.Body, &response); err == nil {
			fmt.Println(convertToJSONString(response))
			return
		}
		fmt.Print(string(result.Body))
		return
	}

	fallback := map[string]interface{}{
		"udid":       result.UDID,
		"action":     result.Action,
		"path":       result.Path,
		"transport":  result.Transport,
		"statusCode": result.StatusCode,
	}
	fmt.Println(convertToJSONString(fallback))
}
