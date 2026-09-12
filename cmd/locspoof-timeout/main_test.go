package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestParseArguments(t *testing.T) {
	seconds, command, arguments, err := parseArguments([]string{"5", "nslookup", "example.com", "127.0.0.1"})
	if err != nil || seconds != 5 || command != "nslookup" || len(arguments) != 2 {
		t.Fatalf("unexpected parse result: %d %q %v %v", seconds, command, arguments, err)
	}
	for _, arguments := range [][]string{{}, {"0", "true"}, {"61", "true"}, {"bad", "true"}} {
		if _, _, _, err := parseArguments(arguments); err == nil {
			t.Fatalf("invalid arguments accepted: %v", arguments)
		}
	}
}

func TestRunCommand(t *testing.T) {
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runCommand(ctx, os.Args[0], []string{"-test.run=TestHelperProcess", "--", "success"}, &stdout, &bytes.Buffer{})
	if err != nil || stdout.String() != "ok" {
		t.Fatalf("successful command: output=%q err=%v", stdout.String(), err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = runCommand(ctx, os.Args[0], []string{"-test.run=TestHelperProcess", "--", "sleep"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out command returned %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "success":
		fmt.Print("ok")
		os.Exit(0)
	case "sleep":
		time.Sleep(2 * time.Second)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}
