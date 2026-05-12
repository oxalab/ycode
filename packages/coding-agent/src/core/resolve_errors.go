package ycode

import "fmt"

type resolveError struct {
	msg string
}

func(e *resolveError) Error() string { return e.msg }

func errResolveFailed(description string, command string) error {
	return &resolveError{msg: fmt.Sprintf("Failed to resolve %s from shell command: %s", description, command)}
}

func errResolveGeneric(description string) error {
	return &resolveError{msg: fmt.Sprintf("Failed to resolve %s", description)}
}

