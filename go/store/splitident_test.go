// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"reflect"
	"testing"
)

func TestSplitCamelSnake(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"fooBar", []string{"foo", "Bar"}},
		{"foo_bar", []string{"foo", "bar"}},
		{"foo_bar_baz", []string{"foo", "bar", "baz"}},
		{"FooBar", []string{"Foo", "Bar"}},
		{"HTTPProxy", []string{"HTTP", "Proxy"}},
		{"getHTTPResponse", []string{"get", "HTTP", "Response"}},
		{"parseConnection", []string{"parse", "Connection"}},
		{"parse_connection_string", []string{"parse", "connection", "string"}},
		{"ID", []string{"ID"}},
		{"parser2", []string{"parser2"}},
		{"v2Decoder", []string{"v2", "Decoder"}},
	}
	for _, c := range cases {
		got := splitCamelSnake(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitCamelSnake(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitIdentifier(t *testing.T) {
	got := splitIdentifier("parseConnection")
	want := "parseConnection parse Connection"
	if got != want {
		t.Errorf("splitIdentifier: got %q want %q", got, want)
	}
}
