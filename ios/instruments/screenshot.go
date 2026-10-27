package instruments

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/bingyuegong/go-ios/ios"
	dtx "github.com/bingyuegong/go-ios/ios/dtx_codec"
	"github.com/bingyuegong/go-ios/ios/golog"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"sync"
	"time"
)

const screenshotServiceName string = "com.apple.instruments.server.services.screenshot"

type ScreenshotService struct {
	channel *dtx.Channel
	conn    *dtx.Connection
}

func NewScreenshotService(device ios.DeviceEntry) (*ScreenshotService, error) {
	golog.DiagnosticInfo("screenshot service connection started", "module", logModule, "udid", device.Properties.SerialNumber)
	dtxConn, err := connectInstruments(device)
	if err != nil {
		return nil, fmt.Errorf("screenshot instruments connection: %w", err)
	}
	processControlChannel, err := dtxConn.RequestChannelIdentifierWithError(screenshotServiceName, loggingDispatcher{dtxConn})
	if err != nil {
		dtxConn.Close()
		return nil, fmt.Errorf("screenshot DTX channel request: %w", err)
	}
	golog.DiagnosticInfo("screenshot service connection completed", "module", logModule, "udid", device.Properties.SerialNumber)
	return &ScreenshotService{channel: processControlChannel, conn: dtxConn}, nil
}

func (d *ScreenshotService) Close() {
	d.conn.Close()
}

func (d *ScreenshotService) TakeScreenshot() ([]byte, error) {
	started := time.Now()
	golog.DiagnosticInfo("screenshot DTX request started", "module", logModule, "selector", "takeScreenshot")
	msg, err := d.channel.MethodCall("takeScreenshot")
	if err != nil {
		golog.DiagnosticError("screenshot DTX request failed", "module", logModule, "duration_ms", time.Since(started).Milliseconds(), "error", err)
		return nil, fmt.Errorf("TakeScreenshot: %w", err)
	}
	if len(msg.Payload) == 0 {
		return nil, fmt.Errorf("TakeScreenshot: device returned empty DTX payload")
	}
	imageBytes, ok := msg.Payload[0].([]byte)
	if !ok || len(imageBytes) == 0 {
		return nil, fmt.Errorf("TakeScreenshot: device returned invalid image payload type %T", msg.Payload[0])
	}
	golog.DiagnosticInfo("screenshot DTX response received", "module", logModule, "duration_ms", time.Since(started).Milliseconds(), "bytes", len(imageBytes))
	return imageBytes, nil
}

// MJPEG server code
var (
	consumers       sync.Map
	conversionQueue = make(chan []byte, 20)
)

func StartMJPEGStreamingServer(device ios.DeviceEntry, port string) error {
	conn, err := NewScreenshotService(device)
	if err != nil {
		return err
	}
	defer conn.Close()

	go startScreenshotting(conn)
	go startConversionQueue()
	http.HandleFunc("/", mjpegHandler)
	location := fmt.Sprintf("0.0.0.0:%s", port)
	golog.Info("starting server, open your browser here", "module", logModule, "udid", device.Properties.SerialNumber, "host", "0.0.0.0", "port", port, "url", fmt.Sprintf("http://%s/", location))
	return http.ListenAndServe(location, nil)
}

func startConversionQueue() {
	var opt jpeg.Options
	opt.Quality = 80

	for {
		pngBytes := <-conversionQueue
		start := time.Now()
		img, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			golog.Warn("failed decoding png", "module", logModule, "error", err)
			continue
		}
		var b bytes.Buffer
		foo := bufio.NewWriter(&b)
		err = jpeg.Encode(foo, img, &opt)
		if err != nil {
			golog.Warn("failed encoding jpg", "module", logModule, "error", err)
			continue
		}
		elapsed := time.Since(start)
		golog.Debug("conversion done", "module", logModule, "seconds", elapsed.Seconds())
		consumers.Range(func(key, value interface{}) bool {
			c := value.(chan []byte)
			go func() { c <- b.Bytes() }()
			return true
		})
	}
}

func startScreenshotting(conn *ScreenshotService) {
	for {
		start := time.Now()
		pngBytes, err := conn.TakeScreenshot()
		if err != nil {
			// Stop the streaming loop instead of killing the host process; a
			// screenshot failure must not take down a caller embedding go-ios.
			golog.Error("screenshot failed, stopping screenshot loop", "module", logModule, "error", err)
			return
		}
		elapsed := time.Since(start)
		golog.Debug("shot done", "module", logModule, "seconds", elapsed.Seconds())
		conversionQueue <- pngBytes
	}
}

const (
	mjpegFrameFooter = "\r\n\r\n"
	mjpegFrameHeader = "--BoundaryString\r\nContent-type: image/jpg\r\nContent-Length: %d\r\n\r\n"
)

func mjpegHandler(w http.ResponseWriter, r *http.Request) {
	golog.Info("starting mjpeg stream for new client", "module", logModule)
	c := make(chan []byte)
	consumers.Store(r, c)
	w.Header().Add("Server", "go-ios-screenshotr-mjpeg-stream")
	w.Header().Add("Connection", "Close")
	w.Header().Add("Content-Type", "multipart/x-mixed-replace; boundary=--BoundaryString")
	w.Header().Add("Max-Age", "0")
	w.Header().Add("Expires", "0")
	w.Header().Add("Cache-Control", "no-cache, private")
	w.Header().Add("Pragma", "no-cache")
	w.Header().Add("Access-Control-Allow-Origin", "*")

	// io.WriteString(w, mjpegStreamHeader)
	w.WriteHeader(200)
	for {
		jpg := <-c
		_, err := io.WriteString(w, fmt.Sprintf(mjpegFrameHeader, len(jpg)))
		if err != nil {
			break
		}
		_, err = w.Write(jpg)
		if err != nil {
			break
		}
		_, err = io.WriteString(w, mjpegFrameFooter)
		if err != nil {
			break
		}
	}
	consumers.Delete(r)
	close(c)
	golog.Info("client disconnected", "module", logModule)
}
