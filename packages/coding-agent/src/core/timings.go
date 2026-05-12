package ycode

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	timingsEnabled 	bool
	timings 		[]timingEntry
	timingsMu 		sync.Mutex
	lastTimingTime 	time.Time	
)

type timingEntry struct {
	Label 	string
	Ms 		int64
}

func init() {
	timingsEnabled = os.Getenv("YCODE_TIMING") == "1"
}

func resetTimings() {
	if !timingsEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	timings = nil
	lastTimingTime = time.Now()
}

func Time(label string){
	if !timingsEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	now := time.Now()
	timings = append(timings, timingEntry{
		Label: label,
		Ms: int64(now.Sub(lastTimingTime).Milliseconds()),
	})
	lastTimingTime = now
}

func PrintTimings() {
	if !timingsEnabled || len(timings) == 0 {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	fmt.Fprintln(os.Stderr, "\n--- Startup Timings ---")
	var total int64
	for _, t := range timings {
		fmt.Fprintf(os.Stderr, "  %s: %dms\n", t.Label, t.Ms)
		total += t.Ms
	}
	fmt.Fprintf(os.Stderr, "  TOTAL: %dms\n", total)
	fmt.Fprintln(os.Stderr, "------------------------")
}