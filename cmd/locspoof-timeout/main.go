package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const maxTimeoutSeconds = 60

func main() {
	seconds, command, arguments, err := parseArguments(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	err = runCommand(ctx, command, arguments, os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		os.Exit(124)
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() >= 0 {
		os.Exit(exitError.ExitCode())
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(125)
}

func parseArguments(arguments []string) (int, string, []string, error) {
	if len(arguments) < 2 {
		return 0, "", nil, fmt.Errorf("usage: locspoof-timeout <seconds> <command> [args...]")
	}
	seconds, err := strconv.Atoi(arguments[0])
	if err != nil || seconds < 1 || seconds > maxTimeoutSeconds {
		return 0, "", nil, fmt.Errorf("timeout must be between 1 and %d seconds", maxTimeoutSeconds)
	}
	return seconds, arguments[1], arguments[2:], nil
}

func runCommand(ctx context.Context, command string, arguments []string, stdout, stderr io.Writer) error {
	process := exec.CommandContext(ctx, command, arguments...)
	process.Stdout = stdout
	process.Stderr = stderr
	err := process.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
