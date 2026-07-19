package tui

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestIsImageLine(t *testing.T) {
	yes := []string{
		"\x1b]1337;File=inline=1:base64==\x07",
		"text \x1b]1337;File=inline=1:base64==\x07 more",
		"\x1b_Ga=T,f=100;data\x1b\\",
		"output: \x1b_Ga=T,f=100;data\x1b\\",
		"\x1b]1337;File=:\x07",
	}
	no := []string{
		"plain text line",
		"\x1b[31mRed text\x1b[0m and \x1b[32mgreen\x1b[0m",
		"\x1b[1A\x1b[2KLine cleared",
		"text with ]1337;File but missing ESC",
		"text with _G but missing ESC",
		"",
		"/path/to/File_1337_backup/image.jpg",
	}
	for _, s := range yes {
		if !IsImageLine(s) {
			t.Errorf("IsImageLine(%q)=false want true", s)
		}
	}
	for _, s := range no {
		if IsImageLine(s) {
			t.Errorf("IsImageLine(%q)=true want false", s)
		}
	}
}

func TestIsImageLineVeryLong(t *testing.T) {
	long := "Text before " + "\x1b]1337;File=size=800,600;inline=1:" + strings.Repeat("A", 300000) + " end"
	if len(long) < 300000 || !IsImageLine(long) {
		t.Error("long image line should be detected")
	}
}

func capsEq(got TerminalCapabilities, images ImageProtocol, tc, hl bool) bool {
	return got.Images == images && got.TrueColor == tc && got.Hyperlinks == hl
}

func TestDetectCapabilities(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		fn   func() bool
		img  ImageProtocol
		tc   bool
		hl   bool
	}{
		{"unknown", nil, nil, "", false, false},
		{"tmux forwards", map[string]string{"TMUX": "/tmp/x", "TERM_PROGRAM": "ghostty"}, func() bool { return true }, "", false, true},
		{"tmux no forward", map[string]string{"TMUX": "/tmp/x", "TERM_PROGRAM": "ghostty"}, func() bool { return false }, "", false, false},
		{"TERM tmux", map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "iterm.app"}, func() bool { return true }, "", false, true},
		{"screen", map[string]string{"TERM": "screen-256color"}, nil, "", false, false},
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, nil, ImageKitty, true, true},
		{"ghostty cmux", map[string]string{"TERM_PROGRAM": "ghostty", "CMUX_WORKSPACE_ID": "ws"}, nil, ImageKitty, true, true},
		{"kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, nil, ImageKitty, true, true},
		{"wezterm", map[string]string{"WEZTERM_PANE": "0"}, nil, ImageKitty, true, true},
		{"warp via term_program", map[string]string{"TERM_PROGRAM": "WarpTerminal"}, nil, ImageKitty, true, true},
		{"warp via session id", map[string]string{"WARP_SESSION_ID": "s"}, nil, ImageKitty, true, true},
		{"warp via uuid", map[string]string{"WARP_TERMINAL_SESSION_UUID": "u"}, nil, ImageKitty, true, true},
		{"warp in tmux", map[string]string{"TERM_PROGRAM": "WarpTerminal", "TMUX": "/tmp/x", "TERM": "tmux-256color"}, func() bool { return true }, "", false, true},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iterm.app"}, nil, ImageITerm2, true, true},
		{"vscode", map[string]string{"TERM_PROGRAM": "vscode"}, nil, "", true, true},
		{"windows terminal", map[string]string{"WT_SESSION": "s", "TERM": "xterm-256color"}, nil, "", true, true},
		{"jetbrains", map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "TERM": "xterm-256color"}, nil, "", true, false},
		{"wt through tmux", map[string]string{"WT_SESSION": "s", "TMUX": "/tmp/x", "TERM": "tmux-256color"}, func() bool { return false }, "", false, false},
		{"colorterm through tmux", map[string]string{"COLORTERM": "truecolor", "TMUX": "/tmp/x", "TERM": "tmux-256color"}, func() bool { return false }, "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withMockEnv(t, c.env, func() {
				got := DetectCapabilities(c.fn)
				if !capsEq(got, c.img, c.tc, c.hl) {
					t.Errorf("got %+v want images=%q tc=%v hl=%v", got, c.img, c.tc, c.hl)
				}
			})
		})
	}
}

func TestEncodeKittyAndDelete(t *testing.T) {
	mc := false
	seq := EncodeKitty("AAAA", KittyEncodeOptions{Columns: 2, Rows: 2, MoveCursor: &mc})
	if !strings.HasPrefix(seq, "\x1b_Ga=T,f=100,q=2,C=1,c=2,r=2;") {
		t.Errorf("encode kitty default+no-cursor: %q", seq)
	}

	if got := DeleteKittyImage(42); got != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
		t.Errorf("delete kitty: %q", got)
	}
	if got := DeleteAllKittyImages(); got != "\x1b_Ga=d,d=A,q=2\x1b\\" {
		t.Errorf("delete all kitty: %q", got)
	}
}

func TestRenderImage(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageKitty, TrueColor: true, Hyperlinks: true})
	prevCell := GetCellDimensions()
	SetCellDimensions(CellDimensions{WidthPx: 10, HeightPx: 10})
	t.Cleanup(func() {
		ResetCapabilitiesCache()
		SetCellDimensions(prevCell)
	})

	// Default (MoveCursor nil) -> no C=1, rows=2.
	seq, rows, _, ok := RenderImage("AAAA", ImageDimensions{WidthPx: 20, HeightPx: 20}, ImageRenderOptions{MaxWidthCells: intPtr(2)})
	if !ok || rows != 2 || strings.Contains(seq, ",C=1,") {
		t.Errorf("default render: ok=%v rows=%d seq=%q", ok, rows, seq)
	}

	// MoveCursor=false -> C=1 present.
	mc := false
	seq, rows, _, ok = RenderImage("AAAA", ImageDimensions{WidthPx: 20, HeightPx: 20}, ImageRenderOptions{MaxWidthCells: intPtr(2), MoveCursor: &mc})
	if !ok || !strings.Contains(seq, ",C=1,") {
		t.Errorf("no-cursor render: ok=%v seq=%q", ok, seq)
	}

	// maxHeight caps rows.
	mh := 5
	seq, rows, _, ok = RenderImage("AAAA", ImageDimensions{WidthPx: 10, HeightPx: 100}, ImageRenderOptions{MaxWidthCells: intPtr(10), MaxHeightCells: &mh})
	if !ok || rows != 5 || !strings.Contains(seq, ",c=1,r=5") {
		t.Errorf("maxHeight render: ok=%v rows=%d seq=%q", ok, rows, seq)
	}

	// No image capability -> ok=false.
	SetCapabilities(TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true})
	if _, _, _, ok := RenderImage("AAAA", ImageDimensions{WidthPx: 10, HeightPx: 10}, ImageRenderOptions{}); ok {
		t.Error("should not render without image capability")
	}
}

func TestHyperlink(t *testing.T) {
	got := Hyperlink("click me", "https://example.com")
	want := "\x1b]8;;https://example.com\x1b\\click me\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	styled := "\x1b[4m\x1b[34mclick me\x1b[0m"
	got = Hyperlink(styled, "https://example.com")
	if !strings.HasPrefix(got, "\x1b]8;;https://example.com\x1b\\") || !strings.HasSuffix(got, "\x1b]8;;\x1b\\") || !strings.Contains(got, styled) {
		t.Errorf("styled hyperlink: %q", got)
	}
	got = Hyperlink("", "https://example.com")
	if got != "\x1b]8;;https://example.com\x1b\\\x1b]8;;\x1b\\" {
		t.Errorf("empty hyperlink: %q", got)
	}
}

func TestImageFallback(t *testing.T) {
	got := ImageFallback("image/jpeg", &ImageDimensions{WidthPx: 800, HeightPx: 600}, "photo.jpg")
	if !strings.Contains(got, "photo.jpg") || !strings.Contains(got, "[image/jpeg]") || !strings.Contains(got, "800x600") {
		t.Errorf("fallback: %q", got)
	}
}

func TestDimensionParsers(t *testing.T) {
	// Minimal PNG header: sig + IHDR with width=10, height=20.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 10, 0, 0, 0, 20}
	dims, ok := GetPNGDimensions(base64.StdEncoding.EncodeToString(png))
	if !ok || dims.WidthPx != 10 || dims.HeightPx != 20 {
		t.Errorf("PNG dims=%+v ok=%v", dims, ok)
	}
	// Minimal GIF: sig + width/height LE.
	gif := []byte{'G', 'I', 'F', '8', '9', 'a', 10, 0, 20, 0}
	dims, ok = GetGIFDimensions(base64.StdEncoding.EncodeToString(gif))
	if !ok || dims.WidthPx != 10 || dims.HeightPx != 20 {
		t.Errorf("GIF dims=%+v ok=%v", dims, ok)
	}
	// Dispatch.
	if _, ok := GetImageDimensions(base64.StdEncoding.EncodeToString(png), "image/png"); !ok {
		t.Error("GetImageDimensions png dispatch failed")
	}
	if _, ok := GetImageDimensions("####", "image/png"); ok {
		t.Error("invalid base64 should fail")
	}
}

func intPtr(v int) *int { return &v }
