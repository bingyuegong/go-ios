package main

import (
	"testing"

	"github.com/docopt/docopt-go"
)

func TestDispatchCommandRunsFirstMatch(t *testing.T) {
	args := docopt.Opts{"alpha": true, "beta": true}
	var ran []string

	handled := dispatchCommand(commandContext{Args: args}, []command{
		commandByBool("alpha", func(commandContext) { ran = append(ran, "alpha") }),
		commandByBool("beta", func(commandContext) { ran = append(ran, "beta") }),
	})

	if !handled {
		t.Fatal("dispatchCommand returned false")
	}
	if len(ran) != 1 || ran[0] != "alpha" {
		t.Fatalf("ran = %#v, want only alpha", ran)
	}
}

func TestDispatchCommandReturnsFalseWithoutMatch(t *testing.T) {
	args := docopt.Opts{"alpha": false}
	handled := dispatchCommand(commandContext{Args: args}, []command{
		commandByBool("alpha", func(commandContext) {
			t.Fatal("handler should not run")
		}),
	})

	if handled {
		t.Fatal("dispatchCommand returned true")
	}
}

func TestDeviceListCommandOnlyMatchesTopLevelList(t *testing.T) {
	if !isDeviceListCommand(docopt.Opts{"list": true}) {
		t.Fatal("top-level list command did not match")
	}

	for _, commandName := range []string{"diagnostics", "image", "devicestate", "profile", "webinspector"} {
		args := docopt.Opts{"list": true, commandName: true}
		if isDeviceListCommand(args) {
			t.Fatalf("list subcommand for %s matched top-level list", commandName)
		}
	}
}

func TestTunnelCommandMatcher(t *testing.T) {
	if !isTunnelCommand(docopt.Opts{"tunnel": true}) {
		t.Fatal("tunnel command did not match")
	}
	if isTunnelCommand(docopt.Opts{"tunnel": false}) {
		t.Fatal("non-tunnel command matched")
	}
}

func TestScreencordCommandsRegisteredAsDeviceCommands(t *testing.T) {
	for _, expected := range []string{"screenrecord_start", "screenrecord_stop", "screenrecord_status"} {
		found := false
		for _, registered := range deviceCommands {
			if registered.name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("device command %q is not registered", expected)
		}
	}
}

func TestPasteboardRegisteredAsDeviceCommand(t *testing.T) {
	for _, registered := range deviceCommands {
		if registered.name == "pasteboard" {
			if !registered.match(docopt.Opts{"pasteboard": true}) {
				t.Fatal("pasteboard command does not match")
			}
			return
		}
	}
	t.Fatal("pasteboard command not registered")
}

func TestKeyboardRegisteredAsDeviceCommand(t *testing.T) {
	for _, registered := range deviceCommands {
		if registered.name == "keyboard" {
			if !registered.match(docopt.Opts{"keyboard": true}) {
				t.Fatal("keyboard command does not match")
			}
			return
		}
	}
	t.Fatal("keyboard command not registered")
}

func TestDeviceIndependentKeyboardCommand(t *testing.T) {
	if !isDeviceIndependentKeyboardCommand(docopt.Opts{"keyboard": true}) {
		t.Fatal("keyboard input must not resolve a new device session")
	}
	if isDeviceIndependentKeyboardCommand(docopt.Opts{"keyboard": false}) {
		t.Fatal("unrelated command treated as keyboard")
	}
}

func TestNeedsAutomaticTunnelInfo(t *testing.T) {
	testCases := []struct {
		name string
		args docopt.Opts
		want bool
	}{
		{name: "zoom stays tunnel-free", args: docopt.Opts{"zoom": true}, want: false},
		{name: "delete testmanagerd recordings needs tunnel", args: docopt.Opts{"delete_all_testmanagerd_screenrecord": true}, want: true},
		{name: "pull testmanagerd recording needs tunnel", args: docopt.Opts{"pull_testmanagerd_screenrecord": true}, want: true},
		{name: "screencord start stays tunnel-free", args: docopt.Opts{"screenrecord_start": true}, want: false},
		{name: "screencord stop stays tunnel-free", args: docopt.Opts{"screenrecord_stop": true}, want: false},
		{name: "screencord status stays tunnel-free", args: docopt.Opts{"screenrecord_status": true}, want: false},
		{name: "voiceover stays tunnel-free", args: docopt.Opts{"voiceover": true}, want: false},
		{name: "assistivetouch stays tunnel-free", args: docopt.Opts{"assistivetouch": true}, want: false},
		{name: "timeformat stays tunnel-free", args: docopt.Opts{"timeformat": true}, want: false},
		{name: "file needs tunnel", args: docopt.Opts{"file": true}, want: true},
		{name: "rsd needs tunnel", args: docopt.Opts{"rsd": true}, want: true},
		{name: "pasteboard needs tunnel", args: docopt.Opts{"pasteboard": true}, want: true},
		{name: "keyboard uses existing daemon without device resolution", args: docopt.Opts{"keyboard": true}, want: false},
		{name: "display info needs tunnel", args: docopt.Opts{"info": true, "display": true}, want: true},
		{name: "plain info stays tunnel-free", args: docopt.Opts{"info": true}, want: false},
		{name: "syslog needs tunnel when available", args: docopt.Opts{"syslog": true}, want: true},
		{name: "runtest needs tunnel on iOS 17", args: docopt.Opts{"runtest": true}, want: true},
		{name: "devicestate needs tunnel (instruments)", args: docopt.Opts{"devicestate": true}, want: true},
		{name: "resetlocation needs tunnel (instruments)", args: docopt.Opts{"resetlocation": true}, want: true},
		{name: "setlocationgpx needs tunnel (instruments)", args: docopt.Opts{"setlocationgpx": true}, want: true},
		{name: "ui run needs tunnel (testmanagerd)", args: docopt.Opts{"ui": true, "run": true}, want: true},
		{name: "ui status stays tunnel-free", args: docopt.Opts{"ui": true, "status": true}, want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := needsAutomaticTunnelInfo(testCase.args); got != testCase.want {
				t.Fatalf("needsAutomaticTunnelInfo() = %t, want %t", got, testCase.want)
			}
		})
	}
}
