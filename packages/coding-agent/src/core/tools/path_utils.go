package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// NOTE: Requires golang.org/x/text/unicode/norm. Currently returns input unchanged
// Without this, macOS NFD-encoded filenames may not match when resolved from user input.
func tryNFDVariant(filePath string) string {
	// TODO: Implement with golang.org/x/text/unicode/norm
	// return norm.NFD.String(filePath)
	return filePath
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

// ResolveToCwd: Resolves a path relative to the fived cwd, handling ~ expanding and absolute paths
func ResolveToCwd(filePath string, cwd string) string {
	expanded := ExpandPath(filePath)
	if filepath.IsAbs(expanded) {
		return expanded
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
1. tryNFDVariant is a no-op — requires golang.org/x/text/unicode/norm external dependency. 
Without it, macOS NFD-encoded filenames (common on HFS+/APFS) won't be resolved. 
The project's go.mod has no external deps yet. 
You'll need to go get golang.org/x/text when you want this working.

2. os.UserHomeDir() can theoretically error — TS os.homedir() never throws. 
In practice on Windows/macOS/Linux it won't, but the error is silently swallowed.
*/