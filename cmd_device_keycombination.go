package main

import (
	"fmt"
	"os"

	"github.com/bingyuegong/go-ios/ios/hidkeyboard"
)

func parseKeyCombination(keys []string) ([]byte, error) {
	return hidkeyboard.ParseKeys(keys)
}

func runKeyCombinationCommand(ctx commandContext) {
	if boolArg(ctx.Args, "daemon") {
		runKeyboardDaemonCommand(ctx)
		return
	}
	keys, ok := ctx.Args["<key>"].([]string)
	if !ok || len(keys) == 0 {
		exitIfError("keyboard: missing keys", fmt.Errorf("use: ios keyboard <key>... -u <udid>"))
		return
	}
	if _, err := parseKeyCombination(keys); err != nil {
		exitIfError("keyboard: invalid keys", err)
		return
	}
	udid, _ := ctx.Args.String("-u")
	if udid == "" {
		udid = os.Getenv("GO_IOS_UDID")
	}
	_, err := sendKeysToDaemon(udid, keys)
	exitIfError("keyboard: failed to send keys", err)
}
