package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/bingyuegong/go-ios/ios/pasteboard"
)

func runPasteboardCommand(ctx commandContext) {
	if !ctx.Device.SupportsRsd() {
		exitIfError("pasteboard requires an iOS 17+ tunnel", fmt.Errorf("start it with ios tunnel start"))
	}
	conn, err := pasteboard.New(ctx.Device)
	exitIfError("pasteboard: failed to connect to service", err)
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("failed to close pasteboard connection", "error", err)
		}
	}()

	if set, _ := ctx.Args.Bool("set"); set {
		var text string
		if ctx.Args["<text>"] != nil {
			text, _ = ctx.Args.String("<text>")
		} else {
			// Only an omitted argument reads stdin; set "" writes empty text.
			data, err := io.ReadAll(os.Stdin)
			exitIfError("pasteboard set: failed to read stdin", err)
			text = string(data)
		}
		exitIfError("pasteboard set: failed to write", conn.SetText(text))
		return
	}

	if get, _ := ctx.Args.Bool("get"); get {
		text, ok, err := conn.GetText()
		exitIfError("pasteboard get: failed to read", err)
		if ok {
			fmt.Println(text)
		}
	}
}
