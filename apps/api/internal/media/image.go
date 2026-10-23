// Package media stores uploaded images on the local persistent volume, records them in media_assets and
// serves them through permission checks (PRD MEDIA-01~08, DD §7, API-DD §5.7).
package media

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder for image.Decode/DecodeConfig
)

// Limits bound what an upload may cost (API-DD §5.7 defaults, configurable).
type Limits struct {
	MaxBytes  int64 // file size
	MaxPixels int64 // width × height
}

const (
	maxSide = 16384 // per-dimension cap, independent of MaxPixels
	// GIF animations are decoded frame by frame into paletted images (1 byte/pixel);
	// cap frames and the total decoded pixels so a tiny file cannot expand into gigabytes.
	maxGIFFrames      = 500
	maxGIFFramePixels = 50_000_000
	thumbMaxSide      = 480
)

type format struct {
	mime     string
	ext      string // canonical stored extension
	decoder  string // name reported by image.DecodeConfig
	thumbExt string // "" = no thumbnail (GIF keeps its animation)
}

var formats = map[string]format{
	"image/jpeg": {"image/jpeg", "jpg", "jpeg", "jpg"},
	"image/png":  {"image/png", "png", "png", "png"},
	"image/webp": {"image/webp", "webp", "webp", "png"},
	"image/gif":  {"image/gif", "gif", "gif", ""},
}

// extensions accepted on the uploaded file name, mapped to the MIME type they must contain.
var extensions = map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp", "gif": "image/gif"}

// Rejection reasons, mapped to HTTP responses by the service.
var (
	errUnsupported  = errors.New("unsupported or disguised file type")
	errTooManyPixel = errors.New("image dimensions exceed the limit")
	errUndecodable  = errors.New("image data cannot be decoded")
)

type inspected struct {
	format format
	width  int
	height int
	frames int
	img    image.Image // decoded still image (nil for GIF), reused for the thumbnail
}

// inspect validates a stored upload: extension, sniffed content type, header dimensions (checked before any
// pixel buffer is allocated), GIF frame budget, and a full decode.
func inspect(f *os.File, filename string, lim Limits) (*inspected, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, errUndecodable
	}
	sniffed := http.DetectContentType(head[:n])
	fm, ok := formats[sniffed]
	if !ok {
		return nil, errUnsupported // SVG, HTML, scripts, archives…
	}
	ext := strings.ToLower(filenameExt(filename))
	if extensions[ext] != fm.mime {
		return nil, errUnsupported // e.g. JPEG bytes named .png, or HTML named .gif
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	cfg, name, err := image.DecodeConfig(f)
	if err != nil || name != fm.decoder {
		return nil, errUndecodable
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errUndecodable
	}
	if cfg.Width > maxSide || cfg.Height > maxSide || int64(cfg.Width)*int64(cfg.Height) > lim.MaxPixels {
		return nil, errTooManyPixel
	}
	out := &inspected{format: fm, width: cfg.Width, height: cfg.Height, frames: 1}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if fm.mime == "image/gif" {
		frames, err := countGIFFrames(f, maxGIFFrames)
		if err != nil {
			return nil, err
		}
		if int64(frames)*int64(cfg.Width)*int64(cfg.Height) > maxGIFFramePixels {
			return nil, errTooManyPixel
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := gif.DecodeAll(f); err != nil {
			return nil, errUndecodable
		}
		out.frames = frames
		return out, nil
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, errUndecodable
	}
	out.img = img
	return out, nil
}

func filenameExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// countGIFFrames walks the GIF block structure without decoding pixels. It stops as soon as the frame
// budget is exceeded, so the cost is bounded by the file size.
func countGIFFrames(r io.Reader, limit int) (int, error) {
	br := bufio.NewReader(r)
	header := make([]byte, 13) // signature + logical screen descriptor
	if _, err := io.ReadFull(br, header); err != nil || (string(header[:6]) != "GIF87a" && string(header[:6]) != "GIF89a") {
		return 0, errUndecodable
	}
	if header[10]&0x80 != 0 {
		if _, err := br.Discard(3 << ((header[10] & 7) + 1)); err != nil {
			return 0, errUndecodable
		}
	}
	skipSubBlocks := func() error {
		for {
			size, err := br.ReadByte()
			if err != nil {
				return err
			}
			if size == 0 {
				return nil
			}
			if _, err := br.Discard(int(size)); err != nil {
				return err
			}
		}
	}
	frames := 0
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, errUndecodable
		}
		switch b {
		case 0x21: // extension: label + data sub-blocks
			if _, err := br.ReadByte(); err != nil || skipSubBlocks() != nil {
				return 0, errUndecodable
			}
		case 0x2C: // image descriptor
			frames++
			if frames > limit {
				return 0, errTooManyPixel
			}
			desc := make([]byte, 9)
			if _, err := io.ReadFull(br, desc); err != nil {
				return 0, errUndecodable
			}
			if desc[8]&0x80 != 0 {
				if _, err := br.Discard(3 << ((desc[8] & 7) + 1)); err != nil {
					return 0, errUndecodable
				}
			}
			if _, err := br.ReadByte(); err != nil || skipSubBlocks() != nil { // LZW minimum code size + data
				return 0, errUndecodable
			}
		case 0x3B: // trailer
			if frames == 0 {
				return 0, errUndecodable
			}
			return frames, nil
		default:
			return 0, errUndecodable
		}
	}
}

// writeThumbnail scales a still image to fit thumbMaxSide. It reports false when no thumbnail is needed
// (GIF animations are served as-is; small images are their own thumbnail). The original is never modified
// (PRD MEDIA-07).
func writeThumbnail(in *inspected, w io.Writer) (bool, error) {
	if in.img == nil || in.format.thumbExt == "" || (in.width <= thumbMaxSide && in.height <= thumbMaxSide) {
		return false, nil
	}
	tw, th := thumbMaxSide, thumbMaxSide
	if in.width >= in.height {
		th = max(1, in.height*thumbMaxSide/in.width)
	} else {
		tw = max(1, in.width*thumbMaxSide/in.height)
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), in.img, in.img.Bounds(), draw.Src, nil)
	var err error
	switch in.format.thumbExt {
	case "jpg":
		err = jpeg.Encode(w, dst, &jpeg.Options{Quality: 82})
	default:
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, dst)
	}
	if err != nil {
		return false, fmt.Errorf("encode thumbnail: %w", err)
	}
	return true, nil
}

func thumbMime(ext string) string {
	if ext == "jpg" {
		return "image/jpeg"
	}
	return "image/png"
}
