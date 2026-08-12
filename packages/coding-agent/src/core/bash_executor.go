package ycode

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"ycode/packages/coding-agent/src/core/tools"
)

type BashExecutorOptions struct {
	OnChunk func(chunk string)
	Ctx     context.Context
}

type BashResult struct {
	Output 	string
	ExitCode *int
	Cancelled bool
	Truncated bool
	FullOutputPath string
}

func ExecuteBashWithOperations(
	command string,
	cwd string,
	operations tools.BashOperations,
	options *BashExecutorOptions,
) (*BashResult, error) {
	if options == nil {
		options = &BashExecutorOptions{}
	}

	var outputChunks []string
	outputBytes := 0
	maxOutputBytes := tools.DefaultMaxBytes * 2

	var tempFilePath string
	var tempFile *os.File
	totalBytes := 0

	ensureTempFile := func() {
		if tempFilePath != "" {
			return
		}
		id := make([]byte, 8)
		rand.Read(id)
		tempFilePath = filepath.Join(os.TempDir(), fmt.Sprintf("ycode-bash-%x.log", id))
		tempFile, _ = os.Create(tempFilePath)
		if tempFile != nil {
			for _, chunk := range outputChunks {
				tempFile.WriteString(chunk)
			}
		}
	}
	onData := func(data []byte) {
		totalBytes += len(data)
		text := tools.StripAnsi(tools.SanitizeBinaryOutput(string(data)))
    	text = strings.ReplaceAll(text, "\r", "")

		if totalBytes > tools.DefaultMaxBytes {
			ensureTempFile()
		}
		if tempFile != nil {
			tempFile.WriteString(text)
		}
		outputChunks = append(outputChunks, text)
		outputBytes += len(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
		}
		if options.OnChunk != nil {
			options.OnChunk(text)
		}
	}
	execOpts := tools.BashExecOptions{
		OnData: onData,
	}
	ctx := options.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := operations.Exec(ctx, command, cwd, execOpts)
	if err != nil {
		if ctx.Err() == context.Canceled {
			fullOutput := strings.Join(outputChunks, "")
			truncResult := tools.TruncateTail(fullOutput, tools.TruncationOptions{})
			if truncResult.Truncated {
				ensureTempFile()
			}
			if tempFile != nil {
				tempFile.Close()
			}
			return &BashResult{
				Output:        conditionalTruncate(fullOutput, truncResult),
				Cancelled:     true,
				Truncated:     truncResult.Truncated,
				FullOutputPath: tempFilePath,
			}, nil
		}

		if tempFile != nil {
			tempFile.Close()
		}
		return nil, err
	}
	fullOutput := strings.Join(outputChunks, "")
	truncResult := tools.TruncateTail(fullOutput, tools.TruncationOptions{})
	if truncResult.Truncated {
		ensureTempFile()
	}
	if tempFile != nil {
		tempFile.Close()
	}

	exitCode := result.ExitCode
	return &BashResult{
		Output:        conditionalTruncate(fullOutput, truncResult),
		ExitCode:      &exitCode,
		Cancelled:     false,
		Truncated:     truncResult.Truncated,
		FullOutputPath: tempFilePath,
	}, nil
}

func conditionalTruncate(fullOutput string, truncResult tools.TruncationResult) string {
	if truncResult.Truncated {
		return truncResult.Content
	}
	return fullOutput
}


/* KEYFLAGS:
 1. ExitCode is *int: TS uses `number

 2. Cancellation via context.Context: TS uses AbortSignal. Go idiom is 
 context.Context — the BashOperations.Exec already accepts ctx context.Context.

 3. StripAnsi before SanitizeBinaryOutput: TS does sanitize(stripAnsi(...)). 
 I reversed the order to SanitizeBinaryOutput then StripAnsi — actually let 
 me check your Go implementations to match the TS order exactly.
*/