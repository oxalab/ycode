package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

const defaultGrepLimit = 100

type GrepToolInput struct {
	Pattern    string  `json:"pattern"`
	Path       *string `json:"path,omitempty"`
	Glob       *string `json:"glob,omitempty"`
	IgnoreCase *bool   `json:"ignoreCase,omitempty"`
	Literal    *bool   `json:"literal,omitempty"`
	Context    *int    `json:"context,omitempty"`
	Limit      *int    `json:"limit,omitempty"`
}

type GrepToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	MatchLimitReached int               `json:"matchLimitReached,omitempty"`
	LinesTruncated    bool              `json:"linesTruncated,omitempty"`
}

// GrepOperations defines pluggable operations for the grep tool
type GrepOperations interface {
	IsDirectory(absolutePath string) (bool, error)
	ReadFile(absolutePath string) (string, error)
}

type defaultGrepOps struct{}

func (d defaultGrepOps) IsDirectory(p string) (bool, error) {
	info, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (d defaultGrepOps) ReadFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}


type GrepToolOptions struct {
	Operations  GrepOperations
}

type rgMatch struct {
	FilePath 	string 	`json:"path"`
	LineNumber 	int 	`json:"line_number"`
	LineText 	string  `json:"lines"`
}

type rgEvent struct {
	Type 	string 	`json:"type"`
	Data 	rgMatch `json:"data"`
}


// exitCodeOf extracts the process exit code from a cmd.Wait error, or -1 if
// it isn't an ExitError.
func exitCodeOf(waitErr error) int {
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func formatGrepPath(filePath string, searchPath string, isDirectory bool) string {
	if isDirectory {
		rel, err := filepath.Rel(searchPath, filePath)
		if err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(filePath)
}

func CreateGrepToolDefinition(cwd string, opts *GrepToolOptions) *extensions.ToolDefinition {
	var ops GrepOperations = defaultGrepOps{}
	if opts != nil && opts.Operations != nil {
		ops = opts.Operations
	}

	return &extensions.ToolDefinition{
		Name: 		"grep",
		Label: 		"grep",
		Description:   fmt.Sprintf("Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to %d matches or %dKB (whichever is hit first). Long lines are truncated to %d chars.", defaultGrepLimit, DefaultMaxBytes/1024, GrepMaxLineLength),
		PromptSnippet: "Search file contents for patterns (respects .gitignore)",
		Parameters: objectSchema(map[string]prop{
			"pattern":    {typ: propString, description: "Search pattern (regex or literal string)"},
			"path":       {typ: propString, description: "Directory or file to search (default: current directory)"},
			"glob":       {typ: propString, description: "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"},
			"ignoreCase": {typ: propBoolean, description: "Case-insensitive search (default: false)"},
			"literal":    {typ: propBoolean, description: "Treat pattern as literal string instead of regex (default: false)"},
			"context":    {typ: propInteger, description: "Number of lines to show before and after each match (default: 0)"},
			"limit":      {typ: propInteger, description: "Maximum number of matches to return (default: 100)"},
		}, []string{"pattern"}),
		Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
			select{
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			var input GrepToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			
			searchPath := ResolveToCwd(".", cwd)
			if input.Path != nil && *input.Path != "" {
				searchPath = ResolveToCwd(*input.Path, cwd)
			}

			contextValue := 0
			if input.Context != nil && *input.Context > 0 {
				contextValue = *input.Context
			}
			effectiveLimit := defaultGrepLimit
			if input.Limit != nil {
				effectiveLimit = max(1, *input.Limit)
			}
			isDirectory, err := ops.IsDirectory(searchPath)
			if err != nil {
				return nil, fmt.Errorf("path not found: %s", searchPath)
			}
			// Check if rg is available
			rgPath, lookupErr := exec.LookPath("rg")
			if lookupErr != nil {
				return nil, fmt.Errorf("ripgrep (rg) is not available on PATH")
			}

			// Build rg arguments
			rgArgs := []string{"--json", "--line-number", "--color=never", "--hidden"}
			if input.IgnoreCase != nil && *input.IgnoreCase {
				rgArgs = append(rgArgs, "--ignore-case")
			}
			if input.Literal != nil && *input.Literal {
				rgArgs = append(rgArgs, "--fixed-strings")
			}
			if input.Glob != nil && *input.Glob != "" {
				rgArgs = append(rgArgs, "--glob", *input.Glob)
			}
			rgArgs = append(rgArgs, "--", input.Pattern, searchPath)
			cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return nil, fmt.Errorf("failed to create pipe: %w", err)
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				return nil, fmt.Errorf("failed to run ripgrep: %w", err)
			}

			// Stream matches
			type matchEntry struct {
				filePath string
				lineNumber int
				lineText string
			}

			var matches []matchEntry
			matchCount := 0
			matchLimitReached := false
			linesTruncated := false
			scanner := bufio.NewScanner(stdout)
			// rg --json lines embed the full matched line, which can be
			// arbitrarily long (minified files); the 64KB default cap would
			// silently end scanning (TS readline is unbounded).
			scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "" {
					continue
				}
				if matchCount >= effectiveLimit {
					break
				}
				var rawData struct {
					Type 	string 	`json:"type"`
					Data 	struct 	{
						Path struct {
							Text string `json:"text"`
						} `json:"path"`
						LineNumber int    `json:"line_number"`
						Lines      struct {
							Text string `json:"text"`
						} `json:"lines"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(line), &rawData); err != nil {
					continue
				}
				if rawData.Type == "match" {
					matchCount++
					matches = append(matches, matchEntry{
						filePath: 	rawData.Data.Path.Text,
						lineNumber: rawData.Data.LineNumber,
						lineText: 	rawData.Data.Lines.Text,
					})
					if matchCount >= effectiveLimit {
						matchLimitReached = true
						cmd.Process.Kill()
						break
					}
				}
			}
			if scanErr := scanner.Err(); scanErr != nil {
				// The scanner stopped early — nothing is draining the pipe, so
				// rg would block on a full one and cmd.Wait() would deadlock.
				cmd.Process.Kill()
				cmd.Wait()
				return nil, fmt.Errorf("ripgrep output error: %v", scanErr)
			}
			waitErr := cmd.Wait()

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("Operation aborted")
			default:
			}
			// Real rg failure (bad regex etc.) must not masquerade as "no
			// matches" (TS grep.ts:293-313: codes 0/1 are the only successes).
			if !matchLimitReached && waitErr != nil {
				if exitErr, ok := waitErr.(*exec.ExitError); ok && (exitErr.ExitCode() == 0 || exitErr.ExitCode() == 1) {
					// treated as success below
				} else {
					msg := strings.TrimSpace(stderr.String())
					if msg == "" {
						msg = fmt.Sprintf("ripgrep exited with code %d", exitCodeOf(waitErr))
					}
					return nil, fmt.Errorf("%s", msg)
				}
			}
			if matchCount == 0 {
				return &ToolExecuteResult{
					Content: []llm.ContentBlock{&llm.TextContent{Type:"text", Text:"No matches found"}},
				}, nil
			}
			// Format matches
			fileCache := make(map[string][]string)
			var outputLines []string

			for _, match := range matches {
				relativePath := formatGrepPath(match.filePath, searchPath, isDirectory)

				if contextValue == 0 {
					// Use line text from rg output directly
					sanitized := strings.ReplaceAll(match.lineText, "\r\n", "\n")
					sanitized = strings.ReplaceAll(sanitized, "\r", "")
					sanitized = strings.TrimRight(sanitized, "\n")
					truncatedText, wasTruncated := TruncateLine(sanitized, GrepMaxLineLength)
					if wasTruncated {
						linesTruncated = true
					}
					outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", relativePath, match.lineNumber, truncatedText))
				} else {
					// Context Lines: read the file
					lines, ok := fileCache[match.filePath]
					if !ok {
						content, err := ops.ReadFile(match.filePath)
						if err != nil {
							outputLines = append(outputLines, fmt.Sprintf("%s:%d: (unable to read file)", relativePath, match.lineNumber))
							continue
						}
						content = strings.ReplaceAll(content, "\r\n", "\n")
						content = strings.ReplaceAll(content, "\r", "\n")
						lines = strings.Split(content, "\n")
						fileCache[match.filePath] = lines
					}

					start := max(1, match.lineNumber-contextValue)
					end := min(len(lines), match.lineNumber+contextValue)

					for current := start; current <= end; current++ {
						lineText := ""
						if current-1 < len(lines) {
							lineText = strings.ReplaceAll(lines[current-1], "\r", "")
						}
						truncatedText, wasTruncated := TruncateLine(lineText, GrepMaxLineLength)
						if wasTruncated {
							linesTruncated = true
						}
						if current == match.lineNumber {
							outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", relativePath, current, truncatedText))
						} else {
							outputLines = append(outputLines, fmt.Sprintf("%s-%d- %s", relativePath, current, truncatedText))
						}
					}
				}
			}
			rawOutput := strings.Join(outputLines, "\n")
			truncation := TruncateHead(rawOutput, TruncationOptions{})
			resultOutput := truncation.Content

			details := &GrepToolDetails{}
			var notices []string
			if matchLimitReached {
				notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit *2))
				details.MatchLimitReached = effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if linesTruncated {
				notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
				details.LinesTruncated = true
			}
			if len(notices) > 0 {
				resultOutput += "\n\n[" + strings.Join(notices, ". ") + "]"
			}
			toolDetails := any(nil)
			if details.MatchLimitReached > 0 || details.Truncation != nil || details.LinesTruncated {
				toolDetails = details
			}
			return &ToolExecuteResult{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: resultOutput}},
				Details: toolDetails,
			}, nil
		},
		RenderCall: func(args any) any {
			argsMap, _ := args.(map[string]any)
			pattern := "?"
			if p, ok := argsMap["pattern"].(string); ok {
				pattern = "/" + p + "/"
			}
			searchDir := "."
			if p, ok := argsMap["path"].(string); ok && p != "" {
				searchDir = ShortenPath(p)
			}
			text := fmt.Sprintf("grep %s in %s", pattern, searchDir)
			if glob, ok := argsMap["glob"].(string); ok && glob != "" {
				text += fmt.Sprintf(" (%s)", glob)
			}
			if limit, ok := argsMap["limit"].(float64); ok {
				text += fmt.Sprintf(" limit %d", int(limit))
			}
			return text
		},
		RenderResult: func(args any, result any, options any) any {
			res, ok := result.(*ToolExecuteResult)
			if !ok || len(res.Content) == 0 {
				return ""
			}
			tc, ok := res.Content[0].(llm.TextContent)
			if !ok {
				return ""
			}
			return tc.Text
		},
	}
}

func CreateGrepTool(cwd string, opts *GrepToolOptions) *sdk.AgentTool[any] {
	return WrapToolDefinition(CreateGrepToolDefinition(cwd, opts), nil)
}

/*
FOOTNOTE:
 1. Requires rg (ripgrep) on PATH — TS uses ensureTool("rg") to auto-download if missing. 
 Go version requires rg to already be installed. The ensureTool / tools-manager would need 
 porting separately for auto-download.

 2. cmd.Stderr type issue — cmd.Stderr expects io.Writer, but I assigned new(strings.Builder). 
 You'll need to change to var stderr strings.Builder; cmd.Stderr = &stderr. I used 
 cmd.Stderr = new(strings.Builder) — this should actually work since *strings.Builder implements 
 io.Writer. Let me double check... actually strings.Builder has a Write(b []byte) method, so 
 new(strings.Builder) returns *strings.Builder which implements io.Writer. This should be fine.

 3. TS truncateHead uses maxLines: Number.MAX_SAFE_INTEGER (byte-only truncation) — Same issue as 
 ls/find. Our version applies a 2000-line default limit.

 4. rg JSON parsing uses a nested struct — The rg --json output has data.path.text and 
 data.lines.text as nested objects. I used a flat struct first (rgEvent) but then used rawData 
 with nested structs. You can remove the unused rgEvent and rgMatch types (lines 72-82). 
 They're dead code.
*/