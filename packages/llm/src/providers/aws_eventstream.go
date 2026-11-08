package providers

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
)

// AWS eventstream binary framing, ported from @smithy/eventstream-codec
// (the locked reference dep of @aws-sdk/client-bedrock-runtime):
//
//	[total length u32 BE][headers length u32 BE][prelude CRC u32]
//	[headers table][payload][message CRC u32]
//
// preludeCRC = CRC32(bytes 0-7); messageCRC = CRC32(bytes 8 .. len-4).
// Both are verified on decode, mirroring the codec's splitMessage().
const (
	awsEventStreamPreludeLen   = 12
	awsEventStreamChecksumLen  = 4
	awsEventStreamMinFrameLen  = awsEventStreamPreludeLen + 2*awsEventStreamChecksumLen
	awsEventStreamMaxFrameLen  = 16 << 20 // 16 MiB hard cap; real converse frames are tiny
	awsEventStreamMaxHeaderLen = 128 << 10
)

// decodeAmazonEventStream reads frames from r and calls fn for each one
// with the parsed (diagnostic) headers and the payload. It returns on the
// first error from fn or from the framing itself (bad CRC/length).
func decodeAmazonEventStream(r io.Reader, fn func(headers map[string]string, body []byte) error) error {
	br := bufio.NewReader(r)

	for {
		frame, err := readAmazonEventStreamFrame(br)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(frame.headers, frame.body); err != nil {
			return err
		}
	}
}

type amazonEventStreamFrame struct {
	headers map[string]string
	body    []byte
}

func readAmazonEventStreamFrame(br *bufio.Reader) (*amazonEventStreamFrame, error) {
	prelude := make([]byte, awsEventStreamPreludeLen)
	if _, err := io.ReadFull(br, prelude); err != nil {
		if err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if totalLen < awsEventStreamMinFrameLen {
		return nil, fmt.Errorf("Provided message too short to accommodate event stream message overhead")
	}
	if totalLen > awsEventStreamMaxFrameLen {
		return nil, fmt.Errorf("Reported message length %d exceeds the maximum accepted frame size", totalLen)
	}
	if headersLen > totalLen {
		return nil, fmt.Errorf("Reported headers length %d exceeds reported message length %d", headersLen, totalLen)
	}

	// Prelude CRC covers the first 8 bytes (the two length fields).
	if got := crc32.ChecksumIEEE(prelude[0:8]); got != preludeCRC {
		return nil, fmt.Errorf("The prelude checksum specified in the message (%08x) does not match the calculated CRC32 checksum (%08x)", preludeCRC, got)
	}

	rest := make([]byte, totalLen-awsEventStreamPreludeLen)
	if _, err := io.ReadFull(br, rest); err != nil {
		if err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("Reported message length does not match received message length")
		}
		return nil, err
	}

	headersEnd := int(headersLen)
	payloadLen := int(totalLen) - awsEventStreamPreludeLen - awsEventStreamChecksumLen - int(headersLen)
	if payloadLen < 0 {
		return nil, fmt.Errorf("Reported headers length %d leaves no room for the payload", headersLen)
	}
	if headersEnd+payloadLen+awsEventStreamChecksumLen != len(rest) {
		return nil, fmt.Errorf("Reported message length does not match received message length")
	}

	headers := rest[:headersEnd]
	body := rest[headersEnd : headersEnd+payloadLen]
	messageCRC := binary.BigEndian.Uint32(rest[headersEnd+payloadLen:])

	// Message CRC: the codec's Crc32 runs cumulatively — updated over bytes
	// 0-7 for the prelude CRC, then continued over bytes 8..len-4 — so the
	// message CRC covers the ENTIRE frame except the trailing 4 bytes.
	full := make([]byte, 0, awsEventStreamPreludeLen+len(rest)-awsEventStreamChecksumLen)
	full = append(full, prelude...)
	full = append(full, rest[:len(rest)-awsEventStreamChecksumLen]...)
	if got := crc32.ChecksumIEEE(full); got != messageCRC {
		return nil, fmt.Errorf("The message checksum (%08x) did not match the expected value (%08x)", messageCRC, got)
	}

	parsedHeaders, err := parseAmazonEventStreamHeaders(headers)
	if err != nil {
		return nil, err
	}

	return &amazonEventStreamFrame{headers: parsedHeaders, body: body}, nil
}

// parseAmazonEventStreamHeaders walks the header table:
// [nameLen u8][name][typeTag u8][value...]. Values are parsed and
// discarded except strings, which are kept for diagnostics (:event-type
// and :content-type are strings).
func parseAmazonEventStreamHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	pos := 0
	for pos < len(data) {
		nameLen := int(data[pos])
		pos++
		if pos+nameLen > len(data) {
			return nil, fmt.Errorf("Unrecognized header: name extends past end of header table")
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen
		if pos >= len(data) {
			return nil, fmt.Errorf("Unrecognized header: missing type tag")
		}
		tag := data[pos]
		pos++

		var skip int
		switch tag {
		case 0, 1: // boolTrue / boolFalse
			skip = 0
		case 2: // byte
			skip = 1
		case 3: // int16
			skip = 2
		case 4: // int32
			skip = 4
		case 5: // int64
			skip = 8
		case 6: // byteArray — u16 length prefix
			if pos+2 > len(data) {
				return nil, fmt.Errorf("Unrecognized header: truncated byteArray length")
			}
			skip = 2 + int(binary.BigEndian.Uint16(data[pos:pos+2]))
		case 7: // string — u16 length prefix
			if pos+2 > len(data) {
				return nil, fmt.Errorf("Unrecognized header: truncated string length")
			}
			strLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2
			if pos+strLen > len(data) {
				return nil, fmt.Errorf("Unrecognized header: string extends past end of header table")
			}
			headers[name] = string(data[pos : pos+strLen])
			pos += strLen
			continue
		case 8: // timestamp (int64)
			skip = 8
		case 9: // uuid
			skip = 16
		default:
			return nil, fmt.Errorf("Unrecognized header type tag")
		}
		if pos+skip > len(data) {
			return nil, fmt.Errorf("Unrecognized header: value extends past end of header table")
		}
		pos += skip
	}
	return headers, nil
}

// encodeAmazonEventStreamFrame is the inverse framing, used by tests to
// synthesize converse-stream responses (checksums computed identically).
func encodeAmazonEventStreamFrame(headers []byte, body []byte) []byte {
	totalLen := uint64(awsEventStreamPreludeLen) + uint64(len(headers)) + uint64(len(body)) + awsEventStreamChecksumLen
	if totalLen > math.MaxUint32 {
		panic("frame too large")
	}
	out := make([]byte, totalLen)
	binary.BigEndian.PutUint32(out[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(out[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(out[8:12], crc32.ChecksumIEEE(out[0:8]))
	copy(out[12:], headers)
	copy(out[12+len(headers):], body)
	// Message CRC is cumulative over the whole frame minus the trailing CRC.
	binary.BigEndian.PutUint32(out[totalLen-awsEventStreamChecksumLen:], crc32.ChecksumIEEE(out[:totalLen-awsEventStreamChecksumLen]))
	return out
}

// encodeAmazonEventStreamStringHeader builds one header-table entry:
// [nameLen u8][name][tag 7][u16 len][utf8].
func encodeAmazonEventStreamStringHeader(name, value string) []byte {
	nameLen := len(name)
	if nameLen > 255 {
		panic("header name too long")
	}
	valueBytes := []byte(value)
	if len(valueBytes) > math.MaxUint16 {
		panic("header value too long")
	}
	out := make([]byte, 0, 1+nameLen+1+2+len(valueBytes))
	out = append(out, byte(nameLen))
	out = append(out, name...)
	out = append(out, 7)
	var lenBytes [2]byte
	binary.BigEndian.PutUint16(lenBytes[:], uint16(len(valueBytes)))
	out = append(out, lenBytes[:]...)
	out = append(out, valueBytes...)
	return out
}

// encodeAmazonEventStreamInt32Header builds [nameLen u8][name][tag 4][i32 BE].
func encodeAmazonEventStreamInt32Header(name string, value int32) []byte {
	nameLen := len(name)
	out := make([]byte, 0, 1+nameLen+1+4)
	out = append(out, byte(nameLen))
	out = append(out, name...)
	out = append(out, 4)
	var valBytes [4]byte
	binary.BigEndian.PutUint32(valBytes[:], uint32(value))
	out = append(out, valBytes[:]...)
	return out
}
