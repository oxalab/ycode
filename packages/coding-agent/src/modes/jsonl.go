// Package modes implements the non-interactive run modes (print + RPC) and the
// JSONL framing they share. This file ports
// reference/coding-agent/src/modes/rpc/jsonl.ts.
package modes

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// SerializeJSONLine serializes value as a single strict JSONL record terminated
// by exactly one LF (TS serializeJsonLine). Framing is LF-only: payload strings
// may contain other Unicode separators (U+2028/U+2029), which clients must split
// only on "\n" to recover.
//
// Go adaptation: TS JSON.stringify returns a string (throwing on failure); Go
// surfaces marshal errors, so this returns (string, error). In practice the RPC
// payloads (commands/responses) are plain structs that never fail to marshal.
//
// Note: encoding/json escapes <, >, &, U+2028, and U+2029 by default. The
// U+2028/2029 escaping actually *strengthens* the single-line guarantee (those
// code points look like line breaks to some tooling); the <,>,& HTML-escaping is
// a minor deviation from TS JSON.stringify (which does not HTML-escape) but
// round-trips identically through any JSON parser.
func SerializeJSONLine(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// AttachJSONLLineReader reads LF-delimited records from r, invoking onLine for
// each framed line, until EOF (TS attachJsonlLineReader). It splits on "\n" only
// and strips a single trailing "\r" per line ("\r\n" → line content).
//
// It intentionally does not use bufio.Scanner with ScanLines for framing
// decisions: while Go's ScanLines happens to split only on "\n"/"\r\n", the
// reference's whole point is strict LF-only framing (Node's readline splits on
// U+2028/U+2029 inside JSON strings), so the explicit "\n" split documents and
// preserves that contract. bufio.Reader.ReadString is unbounded, so records of
// arbitrary length are framed correctly (unlike Scanner's token cap).
//
// Go model deviation: the TS attaches push-model listeners to a Node Readable
// and returns a detach function. Go is pull-based, so this blocks reading r to
// completion (invoking onLine per line) and returns any non-EOF read error in
// place of a detach handle — the analog the RPC stdin loop (20.4) needs.
func AttachJSONLLineReader(r io.Reader, onLine func(line string)) error {
	reader := bufio.NewReader(r)
	for {
		// ReadString includes the trailing "\n" (or returns whatever was read
		// before EOF/error). bufio.Reader.ReadString is unbounded.
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			// Strip the delimiter and a single preceding "\r" ("\r\n" framing),
			// mirroring the TS emitLine that drops a trailing "\r".
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			onLine(line)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
