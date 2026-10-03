package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunWithoutArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: gonixgo") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"frobnicate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDispatches(t *testing.T) {
	commands["test-ok"] = func(args []string, stdout io.Writer) error {
		_, err := io.WriteString(stdout, strings.Join(args, ","))
		return err
	}
	commands["test-fail"] = func([]string, io.Writer) error { return errors.New("boom") }
	defer delete(commands, "test-ok")
	defer delete(commands, "test-fail")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"test-ok", "a", "b"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout.String() != "a,b" {
		t.Fatalf("stdout = %q, want a,b", stdout.String())
	}

	stdout.Reset()
	if code := run([]string{"test-fail"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gonixgo test-fail: boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
