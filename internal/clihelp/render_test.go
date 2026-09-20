package clihelp

import (
	"strings"
	"testing"
)

func TestRender_Global(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("load help catalog: %v", err)
	}
	out, err := c.Render("test-version", "")
	if err != nil {
		t.Fatalf("render global: %v", err)
	}
	if !strings.Contains(out, "go-ios test-version") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "Commands:") {
		t.Fatalf("missing commands section: %q", out)
	}
}

func TestRender_Command(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("load help catalog: %v", err)
	}
	out, err := c.Render("test-version", "apps")
	if err != nil {
		t.Fatalf("render command: %v", err)
	}
	if !strings.Contains(out, "Command: apps") {
		t.Fatalf("missing command title: %q", out)
	}
	if !strings.Contains(out, "ios apps") {
		t.Fatalf("missing command usage: %q", out)
	}
}

func TestRender_ScreencordCommands(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("load help catalog: %v", err)
	}
	for _, command := range []string{"screenrecord_start", "screenrecord_stop", "screenrecord_status"} {
		out, renderErr := c.Render("test-version", command)
		if renderErr != nil {
			t.Fatalf("render %s: %v", command, renderErr)
		}
		for _, expected := range []string{"Command: " + command, "WDA screen recording", "direct usbmux"} {
			if !strings.Contains(out, expected) {
				t.Fatalf("%s help is missing %q: %q", command, expected, out)
			}
		}
	}
}

func TestRender_TestmanagerdScreenrecordCommands(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("load help catalog: %v", err)
	}
	for _, command := range []string{"delete_all_testmanagerd_screenrecord", "pull_testmanagerd_screenrecord"} {
		out, renderErr := c.Render("test-version", command)
		if renderErr != nil {
			t.Fatalf("render %s: %v", command, renderErr)
		}
		for _, expected := range []string{"Command: " + command, "CoreDevice fileservice", "testmanagerd"} {
			if !strings.Contains(out, expected) {
				t.Fatalf("%s help is missing %q: %q", command, expected, out)
			}
		}
	}
}
