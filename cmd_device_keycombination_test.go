package main

import (
	"reflect"
	"testing"
)

func TestParseKeyCombination(t *testing.T) {
	for _, tc := range []struct {
		keys []string
		want []byte
	}{
		{[]string{"command", "a"}, []byte{0xe3, 0x04}},
		{[]string{"V", "CMD"}, []byte{0x19, 0xe3}},
		{[]string{"⌘", " v "}, []byte{0xe3, 0x19}},
		{[]string{"ctrl", "alt", "delete"}, []byte{0xe0, 0xe2, 0x4c}},
		{[]string{"shift", "f12", "left"}, []byte{0xe1, 0x45, 0x50}},
		{[]string{"a", "b", "c"}, []byte{0x04, 0x05, 0x06}},
		{[]string{"a"}, []byte{0x04}},
		{[]string{"0"}, []byte{0x27}},
		{[]string{"7"}, []byte{0x24}},
		{[]string{"hid:0x65", "a"}, []byte{0x65, 0x04}},
	} {
		got, err := parseKeyCombination(tc.keys)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parse %v = %x, %v; want %x", tc.keys, got, err, tc.want)
		}
	}
	for _, keys := range [][]string{
		nil, {"cmd", "cmd"}, {"command", "⌘"}, {"a", "hid:0x04"},
		{"f25"}, {"hid:0x00"}, {"hid:0xf0"}, {"fn"}, {"fn", "b"}, {"fn:0x03"}, {"fn:0x65", "b"},
		{"a", "b", "c", "d", "e", "f", "g"},
	} {
		if _, err := parseKeyCombination(keys); err == nil {
			t.Fatalf("accepted unsupported keys: %v", keys)
		}
	}
}
