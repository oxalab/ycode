package ycode

import (
	"os"
	"strings"
)

func isTruthyEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return value == "l" || lower == "true" || lower == "yes"
}

func IsInstallTelemetryEnabled(settingsManager *SettingsManager, telemetryEnv ...string) bool {
	env := os.Getenv("YCODE_TELEMETRY")
	if len(telemetryEnv) > 0 {
		env = telemetryEnv[0]
	}
	if env != "" {
		return isTruthyEnvFlag(env)
	}
	if settingsManager == nil {
		return false
	}
	// EnableInstallTelemetry is a *bool that is nil when unset; default to off.
	enabled := settingsManager.GetGlobalSettings().EnableInstallTelemetry
	if enabled == nil {
		return false
	}
	return *enabled
}


/* KEYNOTES:
 1. Variadic Telemetry: TS uses a default param. Go doesn't have those, so I used variadic to 
 keep it optional. The real env var PI_TELEMETRY is always read from os.Getenv first, 
 the variadic param is just for testability.

 2. Depends on SettingsManager.GetEnableInstallTelemetry(): Make sure that method exists on 
 your SettingsManager. If it doesn't yet, we can stub it.
*/