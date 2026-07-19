package tui

import "os"

// osGetenv is os.Getenv by default but is a variable so tests can mock
// environment-dependent behavior (e.g. Windows-Terminal/SSH detection).
var osGetenv = os.Getenv
