package tui

// This file ports reference/tui/src/terminal-image.ts — inline-image support
// (Kitty/iTerm2 protocols), terminal capability detection, image dimension
// parsing, and OSC 8 hyperlinks. See mapping/10-terminal-image.md.

import (
	"context"
	"encoding/base64"
	"math"
	"math/rand"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ImageProtocol is the inline-image protocol a terminal supports ("" = none).
type ImageProtocol string

const (
	ImageKitty  ImageProtocol = "kitty"
	ImageITerm2 ImageProtocol = "iterm2"
)

// TerminalCapabilities describes what the attached terminal supports.
type TerminalCapabilities struct {
	Images     ImageProtocol
	TrueColor  bool
	Hyperlinks bool
}

// CellDimensions is the terminal cell size in pixels.
type CellDimensions struct {
	WidthPx  int
	HeightPx int
}

// ImageDimensions is an image's pixel size.
type ImageDimensions struct {
	WidthPx  int
	HeightPx int
}

// ImageRenderOptions controls RenderImage sizing/placement.
type ImageRenderOptions struct {
	MaxWidthCells       *int
	MaxHeightCells      *int
	PreserveAspectRatio *bool
	ImageID             *int
	MoveCursor          *bool // false -> suppress Kitty cursor movement (C=1)
}

var (
	capsMu   sync.Mutex
	cachedCaps *TerminalCapabilities
	cellDims = CellDimensions{WidthPx: 9, HeightPx: 18}
)

const (
	kittyPrefix   = "\x1b_G"
	iterm2Prefix  = "\x1b]1337;File="
)

// GetCellDimensions returns the current cell dimensions (default 9x18; updated by
// the engine from a CSI 16 t response).
func GetCellDimensions() CellDimensions {
	capsMu.Lock()
	defer capsMu.Unlock()
	return cellDims
}

// SetCellDimensions updates the cell dimensions.
func SetCellDimensions(d CellDimensions) {
	capsMu.Lock()
	defer capsMu.Unlock()
	cellDims = d
}

// GetCapabilities returns cached capabilities, detecting them on first use.
func GetCapabilities() TerminalCapabilities {
	capsMu.Lock()
	defer capsMu.Unlock()
	if cachedCaps == nil {
		c := detectCapabilitiesLocked(probeTmuxHyperlinks)
		cachedCaps = &c
	}
	return *cachedCaps
}

// DetectCapabilities forces a fresh detection (useful in tests with mocked env).
func DetectCapabilities(tmuxForwards ...func() bool) TerminalCapabilities {
	fn := probeTmuxHyperlinks
	if len(tmuxForwards) > 0 && tmuxForwards[0] != nil {
		fn = tmuxForwards[0]
	}
	return detectCapabilitiesLocked(fn)
}

// SetCapabilities overrides the cached capabilities (for tests).
func SetCapabilities(caps TerminalCapabilities) {
	capsMu.Lock()
	defer capsMu.Unlock()
	cc := caps
	cachedCaps = &cc
}

// ResetCapabilitiesCache clears the cache so the next GetCapabilities re-detects.
func ResetCapabilitiesCache() {
	capsMu.Lock()
	defer capsMu.Unlock()
	cachedCaps = nil
}

// detectCapabilitiesLocked mirrors reference detectCapabilities (caller may hold capsMu).
func detectCapabilitiesLocked(tmuxForwards func() bool) TerminalCapabilities {
	termProgram := strings.ToLower(osGetenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(osGetenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(osGetenv("TERM"))
	colorTerm := strings.ToLower(osGetenv("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit"

	if osGetenv("TMUX") != "" || strings.HasPrefix(term, "tmux") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: tmuxForwards()}
	}
	if strings.HasPrefix(term, "screen") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
	}
	if osGetenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty" {
		return TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || osGetenv("GHOSTTY_RESOURCES_DIR") != "" {
		return TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true}
	}
	if osGetenv("WEZTERM_PANE") != "" || termProgram == "wezterm" {
		return TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "warpterminal" || osGetenv("WARP_SESSION_ID") != "" || osGetenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true}
	}
	if osGetenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app" {
		return TerminalCapabilities{Images: ImageITerm2, TrueColor: true, Hyperlinks: true}
	}
	if osGetenv("WT_SESSION") != "" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "vscode" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}
	if termProgram == "alacritty" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}
	if terminalEmulator == "jetbrains-jediterm" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: false}
	}
	return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
}

// probeTmuxHyperlinks reports whether the tmux client forwards OSC 8 hyperlinks.
func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Split(string(out), ",") {
		if strings.TrimSpace(f) == "hyperlinks" {
			return true
		}
	}
	return false
}

// IsImageLine reports whether line contains a Kitty or iTerm2 image sequence.
// It uses substring search anywhere in the line (the historical crash fix).
func IsImageLine(line string) bool {
	return strings.Contains(line, kittyPrefix) || strings.Contains(line, iterm2Prefix)
}

// AllocateImageID returns a random Kitty image ID in [1, 0xffffffff].
func AllocateImageID() int {
	return rand.Intn(0xfffffffe) + 1
}

// KittyEncodeOptions controls EncodeKitty.
type KittyEncodeOptions struct {
	Columns   int
	Rows      int
	ImageID   int
	MoveCursor *bool
}

// EncodeKitty encodes base64 image data as a Kitty graphics sequence (chunked).
func EncodeKitty(b64 string, opts KittyEncodeOptions) string {
	const chunkSize = 4096
	var params []string
	params = append(params, "a=T", "f=100", "q=2")
	if opts.MoveCursor != nil && !*opts.MoveCursor {
		params = append(params, "C=1")
	}
	if opts.Columns != 0 {
		params = append(params, "c="+itoa(opts.Columns))
	}
	if opts.Rows != 0 {
		params = append(params, "r="+itoa(opts.Rows))
	}
	if opts.ImageID != 0 {
		params = append(params, "i="+itoa(opts.ImageID))
	}

	if len(b64) <= chunkSize {
		return kittyPrefix + strings.Join(params, ",") + ";" + b64 + "\x1b\\"
	}

	var chunks []string
	offset := 0
	first := true
	for offset < len(b64) {
		end := offset + chunkSize
		isLast := end >= len(b64)
		if end > len(b64) {
			end = len(b64)
		}
		chunk := b64[offset:end]
		switch {
		case first:
			chunks = append(chunks, kittyPrefix+strings.Join(params, ",")+",m=1;"+chunk+"\x1b\\")
			first = false
		case isLast:
			chunks = append(chunks, kittyPrefix+"m=0;"+chunk+"\x1b\\")
		default:
			chunks = append(chunks, kittyPrefix+"m=1;"+chunk+"\x1b\\")
		}
		offset = end
	}
	return strings.Join(chunks, "")
}

// DeleteKittyImage returns the sequence that deletes one Kitty image by ID.
func DeleteKittyImage(id int) string {
	return kittyPrefix + "a=d,d=I,i=" + itoa(id) + ",q=2\x1b\\"
}

// DeleteAllKittyImages returns the sequence that deletes all Kitty images.
func DeleteAllKittyImages() string {
	return kittyPrefix + "a=d,d=A,q=2\x1b\\"
}

// ITerm2EncodeOptions controls EncodeITerm2.
type ITerm2EncodeOptions struct {
	Width               any // int or string
	Height              any
	Name                string
	PreserveAspectRatio *bool
	Inline              *bool
}

// EncodeITerm2 encodes base64 image data as an iTerm2 inline-image sequence.
func EncodeITerm2(b64 string, opts ITerm2EncodeOptions) string {
	var params []string
	inline := true
	if opts.Inline != nil {
		inline = *opts.Inline
	}
	params = append(params, "inline="+b(inline))
	if opts.Width != nil {
		params = append(params, "width="+anyTostr(opts.Width))
	}
	if opts.Height != nil {
		params = append(params, "height="+anyTostr(opts.Height))
	}
	if opts.Name != "" {
		params = append(params, "name="+base64.StdEncoding.EncodeToString([]byte(opts.Name)))
	}
	if opts.PreserveAspectRatio != nil && !*opts.PreserveAspectRatio {
		params = append(params, "preserveAspectRatio=0")
	}
	return iterm2Prefix + strings.Join(params, ";") + ":" + b64 + "\x07"
}

// ImageCellSize is an image's size in terminal cells.
type ImageCellSize struct {
	Columns int
	Rows    int
}

// CalculateImageCellSize computes the cell size of an image scaled to fit.
func CalculateImageCellSize(dims ImageDimensions, maxWidthCells int, maxHeightCells *int, cell CellDimensions) ImageCellSize {
	maxWidth := max(1, maxWidthCells)
	var maxHeight int
	hasMaxHeight := maxHeightCells != nil
	if hasMaxHeight {
		maxHeight = max(1, *maxHeightCells)
	}
	imageWidth := max(1, dims.WidthPx)
	imageHeight := max(1, dims.HeightPx)

	widthScale := float64(maxWidth*cell.WidthPx) / float64(imageWidth)
	heightScale := widthScale
	if hasMaxHeight {
		heightScale = float64(maxHeight*cell.HeightPx) / float64(imageHeight)
	}
	scale := math.Min(widthScale, heightScale)

	scaledWidthPx := float64(imageWidth) * scale
	scaledHeightPx := float64(imageHeight) * scale
	columns := int(math.Ceil(scaledWidthPx / float64(cell.WidthPx)))
	rows := int(math.Ceil(scaledHeightPx / float64(cell.HeightPx)))

	columns = max(1, min(maxWidth, columns))
	if hasMaxHeight {
		rows = max(1, min(maxHeight, rows))
	} else {
		rows = max(1, rows)
	}
	return ImageCellSize{Columns: columns, Rows: rows}
}

// CalculateImageRows returns the number of terminal rows an image occupies at the
// given width.
func CalculateImageRows(dims ImageDimensions, targetWidthCells int, cell CellDimensions) int {
	return CalculateImageCellSize(dims, targetWidthCells, nil, cell).Rows
}

// RenderImage encodes an image for the active terminal, returning the sequence,
// the row count, the image ID (if any), and whether rendering succeeded.
func RenderImage(b64 string, dims ImageDimensions, opts ImageRenderOptions) (seq string, rows int, imageID int, ok bool) {
	caps := GetCapabilities()
	if caps.Images == "" {
		return "", 0, 0, false
	}
	maxWidth := 80
	if opts.MaxWidthCells != nil {
		maxWidth = *opts.MaxWidthCells
	}
	size := CalculateImageCellSize(dims, maxWidth, opts.MaxHeightCells, GetCellDimensions())

	if caps.Images == ImageKitty {
		var kOpts KittyEncodeOptions
		kOpts.Columns = size.Columns
		kOpts.Rows = size.Rows
		if opts.ImageID != nil {
			kOpts.ImageID = *opts.ImageID
		}
		kOpts.MoveCursor = opts.MoveCursor
		return EncodeKitty(b64, kOpts), size.Rows, valueOr(opts.ImageID, 0), true
	}
	if caps.Images == ImageITerm2 {
		iOpts := ITerm2EncodeOptions{
			Width:               size.Columns,
			Height:              "auto",
			PreserveAspectRatio: opts.PreserveAspectRatio,
		}
		if iOpts.PreserveAspectRatio == nil {
			t := true
			iOpts.PreserveAspectRatio = &t
		}
		return EncodeITerm2(b64, iOpts), size.Rows, 0, true
	}
	return "", 0, 0, false
}

// Hyperlink wraps text in an OSC 8 hyperlink (ST-terminated).
func Hyperlink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// ImageFallback returns a text placeholder for unsupported terminals.
func ImageFallback(mime string, dims *ImageDimensions, filename string) string {
	var parts []string
	if filename != "" {
		parts = append(parts, filename)
	}
	parts = append(parts, "["+mime+"]")
	if dims != nil {
		parts = append(parts, itoa(dims.WidthPx)+"x"+itoa(dims.HeightPx))
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}

// ---------------------------------------------------------------------------
// Dimension parsers (from image headers)
// ---------------------------------------------------------------------------

// GetPNGDimensions parses PNG dimensions from base64 image data.
func GetPNGDimensions(b64 string) (ImageDimensions, bool) {
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(buf) < 24 {
		return ImageDimensions{}, false
	}
	if buf[0] != 0x89 || buf[1] != 0x50 || buf[2] != 0x4e || buf[3] != 0x47 {
		return ImageDimensions{}, false
	}
	return ImageDimensions{WidthPx: int(beU32(buf, 16)), HeightPx: int(beU32(buf, 20))}, true
}

// GetJPEGDimensions parses JPEG dimensions from base64 image data.
func GetJPEGDimensions(b64 string) (ImageDimensions, bool) {
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(buf) < 2 {
		return ImageDimensions{}, false
	}
	if buf[0] != 0xff || buf[1] != 0xd8 {
		return ImageDimensions{}, false
	}
	offset := 2
	for offset < len(buf)-9 {
		if buf[offset] != 0xff {
			offset++
			continue
		}
		marker := buf[offset+1]
		if marker >= 0xc0 && marker <= 0xc2 {
			if offset+7 >= len(buf) {
				return ImageDimensions{}, false
			}
			return ImageDimensions{WidthPx: int(beU16(buf, offset+7)), HeightPx: int(beU16(buf, offset+5))}, true
		}
		if offset+3 >= len(buf) {
			return ImageDimensions{}, false
		}
		length := int(beU16(buf, offset+2))
		if length < 2 {
			return ImageDimensions{}, false
		}
		offset += 2 + length
	}
	return ImageDimensions{}, false
}

// GetGIFDimensions parses GIF dimensions from base64 image data.
func GetGIFDimensions(b64 string) (ImageDimensions, bool) {
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(buf) < 10 {
		return ImageDimensions{}, false
	}
	sig := string(buf[0:6])
	if sig != "GIF87a" && sig != "GIF89a" {
		return ImageDimensions{}, false
	}
	return ImageDimensions{WidthPx: int(leU16(buf, 6)), HeightPx: int(leU16(buf, 8))}, true
}

// GetWebPDimensions parses WebP dimensions from base64 image data.
func GetWebPDimensions(b64 string) (ImageDimensions, bool) {
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(buf) < 30 {
		return ImageDimensions{}, false
	}
	if string(buf[0:4]) != "RIFF" || string(buf[8:12]) != "WEBP" {
		return ImageDimensions{}, false
	}
	chunk := string(buf[12:16])
	switch chunk {
	case "VP8 ":
		if len(buf) < 30 {
			return ImageDimensions{}, false
		}
		return ImageDimensions{
			WidthPx:  int(leU16(buf, 26)) & 0x3fff,
			HeightPx: int(leU16(buf, 28)) & 0x3fff,
		}, true
	case "VP8L":
		if len(buf) < 25 {
			return ImageDimensions{}, false
		}
		bits := leU32(buf, 21)
		return ImageDimensions{
			WidthPx:  int(bits&0x3fff) + 1,
			HeightPx: int((bits>>14)&0x3fff) + 1,
		}, true
	case "VP8X":
		if len(buf) < 30 {
			return ImageDimensions{}, false
		}
		return ImageDimensions{
			WidthPx:  int(uint32(buf[24]) | uint32(buf[25])<<8 | uint32(buf[26])<<16) + 1,
			HeightPx: int(uint32(buf[27]) | uint32(buf[28])<<8 | uint32(buf[29])<<16) + 1,
		}, true
	}
	return ImageDimensions{}, false
}

// GetImageDimensions dispatches to the right parser by MIME type.
func GetImageDimensions(b64, mime string) (ImageDimensions, bool) {
	switch mime {
	case "image/png":
		return GetPNGDimensions(b64)
	case "image/jpeg":
		return GetJPEGDimensions(b64)
	case "image/gif":
		return GetGIFDimensions(b64)
	case "image/webp":
		return GetWebPDimensions(b64)
	}
	return ImageDimensions{}, false
}

// ---------------------------------------------------------------------------
// byte helpers
// ---------------------------------------------------------------------------

func beU32(b []byte, i int) uint32 { return uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3]) }
func beU16(b []byte, i int) uint16 { return uint16(b[i])<<8 | uint16(b[i+1]) }
func leU16(b []byte, i int) uint16 { return uint16(b[i]) | uint16(b[i+1])<<8 }
func leU32(b []byte, i int) uint32 { return uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24 }

func itoa(n int) string {
	// strconv.Itoa without the import churn in this file.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func b(x bool) string {
	if x {
		return "1"
	}
	return "0"
}

func anyTostr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return itoa(x)
	case *int:
		return itoa(*x)
	}
	return ""
}

func valueOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
