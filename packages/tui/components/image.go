package components

import (
	"ycode/packages/tui"
)

// ImageTheme styles the image fallback placeholder.
type ImageTheme struct{ FallbackColor tui.StyleFunc }

// ImageOptions configures an Image.
type ImageOptions struct {
	MaxWidthCells  *int
	MaxHeightCells *int
	Filename       string
	ImageID        *int
}

// Image renders an inline image (Kitty/iTerm2) or a text fallback.
type Image struct {
	base64 string
	mime   string
	dims   tui.ImageDimensions
	theme  ImageTheme
	opts   ImageOptions
	imageID *int

	cachedLines []string
	cachedWidth int
}

// NewImage returns an Image. If dims is omitted, it is parsed from the data.
func NewImage(base64Data, mimeType string, theme ImageTheme, opts ImageOptions, dims ...tui.ImageDimensions) *Image {
	var d tui.ImageDimensions
	if len(dims) > 0 {
		d = dims[0]
	} else if parsed, ok := tui.GetImageDimensions(base64Data, mimeType); ok {
		d = parsed
	} else {
		d = tui.ImageDimensions{WidthPx: 800, HeightPx: 600}
	}
	img := &Image{
		base64: base64Data,
		mime:   mimeType,
		dims:   d,
		theme:  theme,
		opts:   opts,
		imageID: opts.ImageID,
	}
	return img
}

// GetImageID returns the Kitty image ID in use (if any).
func (i *Image) GetImageID() *int { return i.imageID }

// Invalidate clears the render cache.
func (i *Image) Invalidate() {
	i.cachedLines = nil
	i.cachedWidth = 0
}

// Render produces the image sequence (+ padding rows) or the fallback text.
func (i *Image) Render(width int) []string {
	if i.cachedLines != nil && i.cachedWidth == width {
		return i.cachedLines
	}

	maxWidth := width - 2
	if i.opts.MaxWidthCells != nil && *i.opts.MaxWidthCells < maxWidth {
		maxWidth = *i.opts.MaxWidthCells
	}
	if maxWidth < 1 {
		maxWidth = 1
	}

	cell := tui.GetCellDimensions()
	defaultMaxHeight := (maxWidth * cell.WidthPx) / cell.HeightPx
	if defaultMaxHeight < 1 {
		defaultMaxHeight = 1
	}
	maxHeight := defaultMaxHeight
	if i.opts.MaxHeightCells != nil {
		maxHeight = *i.opts.MaxHeightCells
	}

	caps := tui.GetCapabilities()
	var lines []string

	moveCursor := false
	if caps.Images != "" {
		if caps.Images == tui.ImageKitty && i.imageID == nil {
			id := tui.AllocateImageID()
			i.imageID = &id
		}
		var imageID *int
		if i.imageID != nil {
			id := *i.imageID
			imageID = &id
		}
		seq, rows, _, ok := tui.RenderImage(i.base64, i.dims, tui.ImageRenderOptions{
			MaxWidthCells:  &maxWidth,
			MaxHeightCells: &maxHeight,
			ImageID:        imageID,
			MoveCursor:     &moveCursor,
		})
		if ok {
			if caps.Images == tui.ImageKitty {
				lines = []string{seq}
				for r := 1; r < rows; r++ {
					lines = append(lines, "")
				}
			} else {
				lines = []string{}
				for r := 1; r < rows; r++ {
					lines = append(lines, "")
				}
				offset := rows - 1
				moveUp := ""
				if offset > 0 {
					moveUp = "\x1b[" + itoaComp(offset) + "A"
				}
				lines = append(lines, moveUp+seq)
			}
			i.cachedLines = lines
			i.cachedWidth = width
			return lines
		}
	}

	fallback := tui.ImageFallback(i.mime, &i.dims, i.opts.Filename)
	styled := fallback
	if i.theme.FallbackColor != nil {
		styled = i.theme.FallbackColor(fallback)
	}
	lines = []string{styled}
	i.cachedLines = lines
	i.cachedWidth = width
	return lines
}

func itoaComp(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
