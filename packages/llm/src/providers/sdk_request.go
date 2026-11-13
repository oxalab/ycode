package providers

import (
	"bytes"
	gocontext "context"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// =============================================================================
// SDK-style request transport (P15: retries/timeout parity)
// =============================================================================
//
// The reference threads options.timeoutMs / options.maxRetries into the
// OpenAI and Anthropic SDK clients per request (anthropic.ts, openai-*.ts),
// inheriting the SDK client defaults when unset. Defaults and behavior below
// are ported from openai@6.26.0 client.js (the @anthropic-ai/sdk is the same
// lineage):
//
//   - maxRetries ?? 2, DEFAULT_TIMEOUT = 600000 /* 10 minutes */
//   - the timeout budget is PER ATTEMPT (each attempt gets a fresh
//     AbortController with the full budget, so a stream trickling for nine
//     minutes still completes)
//   - retryable responses: the x-should-retry header overrides verbatim;
//     otherwise 408, 409, 429 and >=500
//   - connection errors retry too, retries permitting
//   - delay: retry-after-ms (ms) -> retry-after (seconds, then HTTP-date) ->
//     calculateDefaultRetryTimeoutMillis: min(0.5 * 2^attempt, 8) seconds with
//     up to 25% downward jitter
//   - a caller abort cancels the sleep and the in-flight request
//
// Providers whose TS counterparts do not thread these options (google,
// google-vertex, mistral, bedrock) deliberately keep the plain transport.

const (
	sdkRequestMaxRetries = 2
	sdkRequestTimeoutMs  = 600000
)

var (
	errSDKRequestAborted = errors.New("Request was aborted")
	errSDKRequestTimeout = errors.New("Request timed out.")
)

type sdkRequestOptions struct {
	Method     string
	URL        string
	Body       []byte
	Header     http.Header
	Context    gocontext.Context // caller abort signal (StreamOptions.Context)
	Client     *http.Client
	TimeoutMs  int  // <=0 => sdkRequestTimeoutMs
	MaxRetries *int // nil => sdkRequestMaxRetries
}

// doSDKStyleRequest performs the request with SDK-equivalent retry/timeout
// behavior and returns the final response (success or last attempt). The
// caller keeps its own non-2xx status handling. The returned response's body
// releases the per-attempt deadline on EOF/Close, so the stream can outlive
// this call without leaking the timeout timer.
func doSDKStyleRequest(opts sdkRequestOptions) (*http.Response, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = gocontext.Background()
	}
	maxRetries := sdkRequestMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}
	timeoutMs := opts.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = sdkRequestTimeoutMs
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}

	for attempt := 0; ; attempt++ {
		attemptCtx, cancel := gocontext.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		req, err := http.NewRequestWithContext(attemptCtx, opts.Method, opts.URL, bytes.NewReader(opts.Body))
		if err != nil {
			cancel()
			return nil, err
		}
		for key, values := range opts.Header {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil, errSDKRequestAborted
			}
			if errors.Is(err, gocontext.DeadlineExceeded) {
				err = errSDKRequestTimeout
			}
			if attempt < maxRetries {
				if sleepErr := sdkRetrySleep(ctx, nil, attempt); sleepErr != nil {
					return nil, sleepErr
				}
				continue
			}
			return nil, err
		}

		if attempt < maxRetries && sdkShouldRetryResponse(resp) {
			sleepErr := sdkRetrySleep(ctx, resp, attempt)
			_, _ = drainAndClose(resp)
			cancel()
			if sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		// Final attempt: hand the per-attempt deadline to the response body so
		// cancelling waits for EOF/Close instead of killing a good stream.
		resp.Body = &attemptBody{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
}

// sdkShouldRetryResponse mirrors the SDK's shouldRetry: the x-should-retry
// header overrides verbatim; otherwise 408/409/429/>=500.
func sdkShouldRetryResponse(resp *http.Response) bool {
	switch resp.Header.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}
	return resp.StatusCode == 408 || resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500
}

// sdkRetryDelaySeconds resolves the wait before the next attempt.
func sdkRetryDelaySeconds(resp *http.Response, attempt int) float64 {
	if resp != nil {
		if ms := resp.Header.Get("retry-after-ms"); ms != "" {
			if v, err := strconv.ParseFloat(ms, 64); err == nil {
				return v / 1000
			}
		}
		if ra := resp.Header.Get("retry-after"); ra != "" {
			if v, err := strconv.ParseFloat(ra, 64); err == nil {
				return v
			}
			if t, err := http.ParseTime(ra); err == nil {
				return time.Until(t).Seconds()
			}
		}
	}
	sleepSeconds := math.Min(0.5*math.Pow(2, float64(attempt)), 8.0)
	return sleepSeconds * (1 - rand.Float64()*0.25)
}

// sdkRetrySleep waits out the retry delay, aborting with the caller.
func sdkRetrySleep(ctx gocontext.Context, resp *http.Response, attempt int) error {
	delay := time.Duration(sdkRetryDelaySeconds(resp, attempt) * float64(time.Second))
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errSDKRequestAborted
	case <-timer.C:
		return nil
	}
}

func drainAndClose(resp *http.Response) (int64, error) {
	n, err := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return n, err
}

// attemptBody releases the per-attempt context when the response body is fully
// consumed (or closed early).
type attemptBody struct {
	io.ReadCloser
	cancel gocontext.CancelFunc
}

func (b *attemptBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.cancel()
	}
	return n, err
}

func (b *attemptBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
