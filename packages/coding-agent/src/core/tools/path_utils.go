package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var unicodeSpacesRe = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")

const narrowNoBreakSpace = "\u202F"

func normalizeUnicodeSpaces(str string) string {
	return unicodeSpacesRe.ReplaceAllString(str, " ")
}

func tryMacOSScreenshotPath(filePath string) string {
	re := regexp.MustCompile(`(?i) (AM|PM)\.`)
	return re.ReplaceAllString(filePath, narrowNoBreakSpace+"$1.")
}

// tryNFDVariant: Attempts NFD Unicode normalization for macOS compatibility.
// macOS stores filenames in NFD (decomposed) form, so user input in NFC
// (composed) form won't match — try converting it (TS: filePath.normalize("NFD")).
func tryNFDVariant(filePath string) string {
	return norm.NFD.String(filePath)
}

func tryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "\u2019")
}

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

func normalizeAtPrefix(filePath string) string {
	if strings.HasPrefix(filePath, "@") {
		return filePath[1:]
	}
	return filePath
}

// ExpandPath handles ~ expansion and unicode space normalization.
func ExpandPath(filePath string) string {
	normalized := normalizeUnicodeSpaces(normalizeAtPrefix(filePath))
	if normalized == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(normalized, "~/") {
		home, _ := os.UserHomeDir()
		return home + normalized[1:]
	}
	return normalized
}

// isWindowsRootRelative: Reports whether p is root-relative on Windows —
// starts with a separator but carries no volume (e.g. "/foo" or "\foo").
// Go's filepath.IsAbs says false for these (unlike TS path.isAbsolute), so
// ResolveToCwd must special-case them instead of joining them under cwd.
func isWindowsRootRelative(p string) bool {
	if runtime.GOOS != "windows" || filepath.IsAbs(p) {
		return false
	}
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\")
}

// ResolveToCwd: Resolves a path relative to the fived cwd, handling ~ expanding and absolute paths
func ResolveToCwd(filePath string, cwd string) string {
	expanded := ExpandPath(filePath)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	if isWindowsRootRelative(expanded) {
		// "/foo" anchors to the drive root (TS: path.isAbsolute("/foo") is true on
		// Windows); resolve against the cwd's drive like Node's path.resolve(cwd, "/foo").
		return filepath.Join(filepath.VolumeName(cwd), expanded)
	}
	return filepath.Join(cwd, expanded)
}

// ResolveReadPath: Resolved a path for reading, trying multiple variants for macOS compatibility
func ResolveReadPath(filePath string, cwd string) string {
	resolved := ResolveToCwd(filePath, cwd)
	if fileExists(resolved) {
		return resolved
	}

	// Try macOS AM/PM variant (narrow no-break space before AM/PM)
	amPmVariant := tryMacOSScreenshotPath(resolved)
	if amPmVariant != resolved && fileExists(amPmVariant) {
		return amPmVariant
	}

	// Try NFD Variant (macOS stores filenames in NFD form)
	nfdVariant := tryNFDVariant(resolved)
	if nfdVariant != resolved && fileExists(nfdVariant) {
		return nfdVariant
	}

	// Try curly quote variant (macOS uses U+2019 in screenshot names)
	curlyVariant := tryCurlyQuoteVariant(resolved)
	if curlyVariant != resolved && fileExists(curlyVariant) {
		return curlyVariant
	}
	// Try combined NFD + curly quote (e.g. "Capture d'écran")
	nfdCurlyVariant := tryCurlyQuoteVariant(nfdVariant)
	if nfdCurlyVariant != resolved && fileExists(nfdCurlyVariant) {
		return nfdCurlyVariant
	}
	return resolved
}



/*
Footnote:
Flags:
1. os.UserHomeDir() can theoretically error — TS os.homedir() never throws.
In practice on Windows/macOS/Linux it won't, but the error is silently swallowed.
*/