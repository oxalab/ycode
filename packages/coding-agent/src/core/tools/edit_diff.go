package tools

import (
	"fmt"
	"strconv"
	"strings"
	"os"
)

// --- Line ending utilities ---

func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\r\n"
	}
	return "\n"
}

func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func RestoreLineEndings(text string, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// --- Fuzzy Matching ----

/*
NormalizeForFuzzyMatch: Normalized text for fuzzy matching.
 - Applies progressive transformations: strip trailing whitespaces, normalize smart quotes, Unicode dashes and special cases.
 - NOTE: NKFC Normalization (.normalize("NKFC") in TS) is not applied.
 - Requires golang.org/x/text/unicode/norm. Without it, fullwidth/compatibility characters
 - won't be matched. Rare in code editing contexts.
*/
func NormalizeForFuzzyMatch(text string) string {
	// Strip trailing whitespace per line
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\t")
	}
	result := strings.Join(lines, "\n")
	// Normalize characters via rune mapping
	result = strings.Map(normalizeFuzzyRune, result)
	return result
}

func normalizeFuzzyRune(r rune) rune {
	// Smart single quotes → '
	if r == '\u2018' || r == '\u2019' || r == '\u201A' || r == '\u201B' {
		return '\''
	}
	// Smart double quotes → "
	if r == '\u201C' || r == '\u201D' || r == '\u201E' || r == '\u201F' {
		return '"'
	}
	// Various dashes/hyphens → -
	if r == '\u2010' || r == '\u2011' || r == '\u2012' || r == '\u2013' || r == '\u2014' || r == '\u2015' || r == '\u2212' {
		return '-'
	}
	// Special spaces → regular space
	if r == '\u00A0' || (r >= '\u2002' && r <= '\u200A') || r == '\u202F' || r == '\u205F' || r == '\u3000' {
		return ' '
	}
	return r
}

// --- Fuzzy Find ---

type FuzzyMatchResult struct {
	Found 		bool
	Index 		int
	MatchLength int
	UsedFuzzyMatch bool
	ContentForReplacement string
}

func FuzzyFindText(content string, oldText string) FuzzyMatchResult {
	// Try exact match first
	exactIndex := strings.Index(content, oldText)
	if exactIndex != - 1{
		return FuzzyMatchResult{
			Found: 			true,
			Index: 			exactIndex,
			MatchLength: 	len(oldText),
			UsedFuzzyMatch: 	false,
			ContentForReplacement: content,
		}
	}

	// Try fuzzy match
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)

	if fuzzyIndex == -1 {
		return FuzzyMatchResult{
			Found:                 false,
			Index:                 -1,
			MatchLength:           0,
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}

	return FuzzyMatchResult{
		Found:                 true,
		Index:                 fuzzyIndex,
		MatchLength:           len(fuzzyOldText),
		UsedFuzzyMatch:        true,
		ContentForReplacement: fuzzyContent,
	}
}

// --- BOM ---

func StripBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[1:]
	}
	return "", content
}

// --- Edit Types ---

type Edit struct {
	OldText string
	NewText string
}

type AppliedEditsResult struct {
	BaseContent string
	NewContent string
}

// --- Error Helpers ---

func getNotFoundError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Could not find the exact text in %s. THe old text must match exactly including all whitespaces and newlines", path)
	}
	return fmt.Errorf("could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines", editIndex, path)
}

func getDuplicateError(path string, editIndex int, totalEdits int, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf("found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique", occurrences, path)
	}
	return fmt.Errorf("found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique", occurrences, editIndex, path)
}

func getEmptyOldTextError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s", editIndex, path)
}

func getNoChangeError(path string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("no changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected", path)
	}
	return fmt.Errorf("no changes made to %s. The replacements produced identical content", path)
}

// --- Count Occurences ---

func countOccurences(content string, oldText string) int {
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	return strings.Count(fuzzyContent, fuzzyOldText)
}

// --- Apply Edits ---

type matchedEdit struct {
	editIndex 	int
	matchIndex 	int
	matchLength int
	newText 	string
}

// ApplyEditsToNormalizedContent: Applies one or more exact-text replacement to LF-normalized content
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (AppliedEditsResult, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{
			OldText: NormalizeToLF(edit.OldText),
			NewText: NormalizeToLF(edit.NewText),
		}
	}
	for i, edit := range normalizedEdits {
		if len(edit.OldText) == 0 {
			return AppliedEditsResult{}, getEmptyOldTextError(path, i, len(normalizedEdits))
		}
	}
	initialMatches := make([]FuzzyMatchResult, len(normalizedEdits))
	for i, edit := range normalizedEdits {
		initialMatches[i] = FuzzyFindText(normalizedContent, edit.OldText)
	}

	needsFuzzy := false
	for _, match := range initialMatches {
		if match.UsedFuzzyMatch {
			needsFuzzy = true
			break
		}
	}

	baseContent := normalizedContent
	if needsFuzzy {
		baseContent = NormalizeForFuzzyMatch(normalizedContent)
	}

	var matchedEdits []matchedEdit
	for i, edit := range normalizedEdits {
		matchResult := FuzzyFindText(baseContent, edit.OldText)
		if !matchResult.Found {
			return AppliedEditsResult{}, getNotFoundError(path, i, len(normalizedEdits))
		}

		occurences := countOccurences(baseContent, edit.OldText)
		if occurences > 1 {
			return AppliedEditsResult{}, getDuplicateError(path, i, len(normalizedEdits), occurences)
		}
		matchedEdits = append(matchedEdits, matchedEdit{
			editIndex: i,
			matchIndex: matchResult.Index,
			matchLength: matchResult.MatchLength,
			newText: edit.NewText,
		})
	}

	// Sort by match index
	for i := 1; i < len(matchedEdits); i++ {
		for j := i; j > 0 && matchedEdits[j].matchIndex < matchedEdits[j-i].matchIndex; j-- {
			matchedEdits[j], matchedEdits[j-1] = matchedEdits[j-1], matchedEdits[j]
		}
	}

	// Check for overlaps
	for i := 1; i < len(matchedEdits); i++ {
		prev := matchedEdits[i-1]
		curr := matchedEdits[i]
		if prev.matchIndex+prev.matchLength > curr.matchIndex {
			return AppliedEditsResult{}, fmt.Errorf("edits[%d] and edits[%d] overalp in %s. Merge them into one edit or target disjoint regions", prev.editIndex, curr.editIndex, path)
		}
	}

	// Apply in reverse order so offsets remain stable
	newContent := baseContent
	for i := len(matchedEdits) - 1; i >= 0; i-- {
		edit := matchedEdits[i]
		newContent = newContent[:edit.matchIndex] + edit.newText + newContent[edit.matchIndex+edit.matchIndex:]
	}
	if baseContent == newContent {
		return AppliedEditsResult{}, getNoChangeError(path, len(normalizedEdits))
	}
	return AppliedEditsResult{BaseContent: baseContent, NewContent: newContent}, nil
}

// --- Diff generation (ports npm "diff" package diffLines) ---

type diffPart struct {
	value 	string
	added 	bool
	removed bool
}

const maxDiffLines = 5000

func diffLinesCompute(oldStr, newStr string) []diffPart {
	oldLines := splitForDiff(oldStr)
	newLines := splitForDiff(newStr)

	if len(oldLines) == 0 && len(newLines) == 0 {
		return nil
	}
	if len(oldLines) == 0 {
		return []diffPart{{value: strings.Join(newLines, "\n") + "\n", added: true}}
	}
	if len(newLines) == 0 {
		return []diffPart{{value: strings.Join(oldLines, "\n") + "\n", removed: true}}
	}

	// Fall back to simple diff for very large files
	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		var parts []diffPart
		if len(oldLines) > 0 {
			parts = append(parts, diffPart{value: strings.Join(oldLines, "\n") + "\n", removed: true})
		}
		if len(newLines) > 0 {
			parts = append(parts, diffPart{value: strings.Join(newLines, "\n") + "\n", added: true})
		}
		return parts
	}

	m, n := len(oldLines), len(newLines)
	
	// LCS DP Table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; i <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack to generate diff parts
	var parts []diffPart
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			parts = append(parts, diffPart{value: oldLines[i-1]+ "\n"})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			parts = append(parts, diffPart{value: newLines[j-1] + "\n", added: true})
			j--
		} else {
			parts = append(parts, diffPart{value: oldLines[i-1] + "\n", removed: true})
			i--
		}
	}

	// Reverse (we backtracked)
	for l, r := 0, len(parts) - 1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return mergeDiffParts(parts)
}

func splitForDiff(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func mergeDiffParts(parts []diffPart) []diffPart {
	if len(parts) == 0 {
		return parts
	}
	merged := []diffPart{parts[0]}
	for i := 1; i < len(parts); i++ {
		last := &merged[len(merged)-1]
		if parts[i].added == last.added && parts[i].removed == last.removed {
			last.value += parts[i].value
		} else {
			merged = append(merged, parts[i])
		}
	}
	return merged
}

// --- Generate Diff String ---

type EditDiffResult struct {
	Diff 		string
	FirstChangedLine int
}

type EditDiffError struct {
	Error string
}

/*
GenerateDiffString: Generates a unified diff string with line number and context.
 - Returns the diff string and the first changed line number (in the new file), or -1 if none.
*/
func GenerateDiffString(oldContent, newContent string, contextLines int) (string, int) {
	parts := diffLinesCompute(oldContent, newContent)
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := max(len(oldLines), len(newLines))
	lineNumWidth := len(strconv.Itoa(maxLineNum))

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	firstChangedLine := -1

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		raw := strings.Split(part.value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.added || part.removed {
			if firstChangedLine == -1 {
				firstChangedLine = newLineNum
			}

			for _, line := range raw {
				if part.added {
					lineNum := fmt.Sprintf("%*d", lineNumWidth, newLineNum)
					output = append(output, "+"+lineNum+" "+line)
					newLineNum++
				} else {
					lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
					output = append(output, "-"+lineNum+" "+line)
					oldLineNum++
				}
			}
			lastWasChange = true
		} else {
			nextPartIsChange := i < len(parts)-1 && (parts[i+1].added || parts[i+1].removed)
			hasLeadingChange := lastWasChange
			hasTrailingChange := nextPartIsChange

			if hasLeadingChange && hasTrailingChange {
				if len(raw) <= contextLines*2 {
					for _, line := range raw {
						lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}
				} else {
					leadingLines := raw[:contextLines]
					trailingLines := raw[len(raw)-contextLines:]
					skippedLines := len(raw) - len(leadingLines) - len(trailingLines)

					for _, line := range leadingLines {
						lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}

					output = append(output, " "+fmt.Sprintf("%*s", lineNumWidth, "")+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines

					for _, line := range trailingLines {
						lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}
				}
			} else if hasLeadingChange {
				shownCount := min(len(raw), contextLines)
				shownLines := raw[:shownCount]
				skippedLines := len(raw) - shownCount

				for _, line := range shownLines {
					lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
					output = append(output, " "+lineNum+" "+line)
					oldLineNum++
					newLineNum++
				}

				if skippedLines > 0 {
					output = append(output, " "+fmt.Sprintf("%*s", lineNumWidth, "")+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}
			} else if hasTrailingChange {
				skippedLines := len(raw) - contextLines
				if skippedLines > 0 {
					output = append(output, " "+fmt.Sprintf("%*s", lineNumWidth, "")+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}

				start := max(0, skippedLines)
				for _, line := range raw[start:] {
					lineNum := fmt.Sprintf("%*d", lineNumWidth, oldLineNum)
					output = append(output, " "+lineNum+" "+line)
					oldLineNum++
					newLineNum++
				}
			} else {
				oldLineNum += len(raw)
				newLineNum += len(raw)
			}

			lastWasChange = false
		}
	}

	return strings.Join(output, "\n"), firstChangedLine
}

// --- Compute diff (top-level API) ---

func ComputeEditsDiff(path string, edits []Edit, cwd string) (EditDiffResult, EditDiffError) {
	absolutePath := ResolveToCwd(path, cwd)

	// Check if file exists and is readable
	info, err := os.Stat(absolutePath)
	if err != nil {
		errMsg := fmt.Sprintf("Could not edit file: %s. Error code: %s", path, err.Error())
		return EditDiffResult{}, EditDiffError{Error: errMsg}
	}
	if info.IsDir() {
		return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Path is a directory.", path)}
	}

	// Read the file
	rawContent, err := os.ReadFile(absolutePath)
	if err != nil {
		return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not read file: %s. %s", path, err.Error())}
	}

	rawStr := string(rawContent)
	_, content := StripBom(rawStr)
	normalizedContent := NormalizeToLF(content)

	result, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
	if err != nil {
		return EditDiffResult{}, EditDiffError{Error: err.Error()}
	}

	diff, firstChangedLine := GenerateDiffString(result.BaseContent, result.NewContent, 4)
	return EditDiffResult{Diff: diff, FirstChangedLine: firstChangedLine}, EditDiffError{}
}

func ComputeEditDiff(path string, oldText string, newText string, cwd string) (EditDiffResult, EditDiffError) {
	return ComputeEditsDiff(path, []Edit{{OldText: oldText, NewText: newText}}, cwd)
}




/*
FOOTNOTES:
 1. npm diff package replaced with custom LCS-based diff — The TS reference uses the diff 
 npm package (Myers algorithm). Our Go version uses LCS-based line diff. Produces valid diffs 
 but may differ slightly in edge cases (e.g., tie-breaking between adds/removes). Files over 
 5000 lines fall back to a simple "everything changed" diff.

 2. NFKC normalization missing (same as path-utils) — normalizeForFuzzyMatch does all the 
 ASCII-space normalizations (quotes, dashes, spaces, trailing whitespace) but 
 skips .normalize("NFKC"). Fullwidth characters (U+FF01 etc.) won't be matched. 
 Requires golang.org/x/text/unicode/norm.

 3. ComputeEditsDiff is synchronous in Go — The TS version is async (reads file asynchronously). 
 Go's os.ReadFile is synchronous. Same end result, different concurrency model. The caller can 
 wrap in a goroutine if needed.
*/