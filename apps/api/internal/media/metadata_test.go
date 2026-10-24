package media

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	_ "golang.org/x/image/webp"
)

// Fake private metadata that must disappear.
const secret = "GPSLatitude 39.9042N Serial#X123 Author:Alice"

func testImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 40), uint8(y * 40), 120, 255})
		}
	}
	return img
}

// exifAPP1 builds an APP1 EXIF segment with an Orientation tag and the secret as an extra ASCII payload.
func exifAPP1(orientation int) []byte {
	tiff := []byte{'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, 0x01, 0x00,
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, byte(orientation), 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00}
	payload := append(append([]byte("Exif\x00\x00"), tiff...), secret...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func segment(marker byte, payload []byte) []byte {
	seg := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func decodeBytes(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return img
}

func samePixels(t *testing.T, a, b image.Image) {
	t.Helper()
	if a.Bounds() != b.Bounds() {
		t.Fatalf("bounds %v vs %v", a.Bounds(), b.Bounds())
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				t.Fatalf("pixel %d,%d changed", x, y)
			}
		}
	}
}

func TestStripJPEG(t *testing.T) {
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, testImage(6, 4), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	plain := enc.Bytes()
	icc := segment(0xE2, []byte("ICC_PROFILE\x00\x01\x01fake-profile"))
	xmp := segment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), secret...))
	iptc := segment(0xED, append([]byte("Photoshop 3.0\x00"), secret...))
	com := segment(0xFE, []byte(secret))
	// SOI + metadata + the rest of the encoder output (its own APP0/DQT/SOF/…/SOS).
	dirty := append(append([]byte{0xFF, 0xD8}, append(append(append(append(exifAPP1(6), xmp...), iptc...), com...), icc...)...), plain[2:]...)
	p := writeTemp(t, "a.jpg", dirty)

	o, err := stripMetadata(p, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	clean, _ := os.ReadFile(p)
	if o != 6 {
		t.Fatalf("orientation %d", o)
	}
	if bytes.Contains(clean, []byte("GPS")) || bytes.Contains(clean, []byte("Alice")) || bytes.Contains(clean, []byte("ns.adobe.com")) {
		t.Fatal("private metadata left in JPEG")
	}
	if !bytes.Contains(clean, []byte("ICC_PROFILE")) {
		t.Fatal("ICC profile dropped")
	}
	if exifOrientation(clean[bytes.Index(clean, []byte("Exif\x00\x00"))+6:]) != 6 {
		t.Fatal("orientation not preserved")
	}
	// Everything from SOS on is byte-identical: no re-encoding.
	sos := bytes.Index(plain, []byte{0xFF, 0xDA})
	if !bytes.HasSuffix(clean, plain[sos:]) {
		t.Fatal("entropy-coded data changed")
	}
	samePixels(t, decodeBytes(t, plain), decodeBytes(t, clean))

	// Orientation 1 and no EXIF: no EXIF block is added.
	p2 := writeTemp(t, "b.jpg", plain)
	if o, err := stripMetadata(p2, "image/jpeg"); err != nil || o != 1 {
		t.Fatalf("plain jpeg: %d %v", o, err)
	}
	if b, _ := os.ReadFile(p2); !bytes.Equal(b, plain) {
		t.Fatal("clean JPEG was modified")
	}
	if _, err := stripMetadata(writeTemp(t, "c.jpg", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}), "image/jpeg"); err == nil {
		t.Fatal("truncated JPEG accepted")
	}
}

func pngChunk(typ string, data []byte) []byte {
	c := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(c, uint32(len(data)))
	copy(c[4:], typ)
	c = append(c, data...)
	return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(c[4:]))
}

func TestStripPNG(t *testing.T) {
	var enc bytes.Buffer
	if err := png.Encode(&enc, testImage(5, 5)); err != nil {
		t.Fatal(err)
	}
	plain := enc.Bytes()
	ihdrEnd := 8 + 12 + 13
	extra := append(append(append(pngChunk("tEXt", []byte("Comment\x00"+secret)), pngChunk("iTXt", []byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"+secret))...),
		pngChunk("eXIf", append([]byte("MM\x00\x2A"), secret...))...), pngChunk("tIME", []byte{7, 234, 10, 24, 12, 0, 0})...)
	dirty := append(append(append([]byte{}, plain[:ihdrEnd]...), extra...), plain[ihdrEnd:]...)
	p := writeTemp(t, "a.png", dirty)
	if _, err := stripMetadata(p, "image/png"); err != nil {
		t.Fatal(err)
	}
	clean, _ := os.ReadFile(p)
	if !bytes.Equal(clean, plain) {
		t.Fatal("PNG not restored to the plain encoder output")
	}
}

// 1×1 lossless WebP (VP8L) from the libwebp test corpus.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func riffChunk(id string, data []byte) []byte {
	c := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(c[4:], uint32(len(data)))
	c = append(c, data...)
	if len(data)%2 == 1 {
		c = append(c, 0)
	}
	return c
}

func TestStripWebP(t *testing.T) {
	simple, _ := base64.StdEncoding.DecodeString(tinyWebP)
	vp8l := simple[12:] // the VP8L chunk
	vp8x := make([]byte, 10)
	vp8x[0] = 0x08 | 0x04 // EXIF + XMP flags; canvas 1×1 (stored minus one)
	body := append(append(append(append([]byte("WEBP"), riffChunk("VP8X", vp8x)...), vp8l...), riffChunk("EXIF", []byte(secret))...), riffChunk("XMP ", []byte(secret))...)
	dirty := append(append([]byte("RIFF"), 0, 0, 0, 0), body...)
	binary.LittleEndian.PutUint32(dirty[4:], uint32(len(body)))
	p := writeTemp(t, "a.webp", dirty)
	if _, err := stripMetadata(p, "image/webp"); err != nil {
		t.Fatal(err)
	}
	clean, _ := os.ReadFile(p)
	if bytes.Contains(clean, []byte("GPS")) || bytes.Contains(clean, []byte("EXIF")) || bytes.Contains(clean, []byte("XMP ")) {
		t.Fatal("WebP metadata left")
	}
	if int(binary.LittleEndian.Uint32(clean[4:])) != len(clean)-8 || clean[20]&0x0C != 0 {
		t.Fatalf("RIFF size or VP8X flags not fixed: % x", clean[:24])
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(clean)); err != nil {
		t.Fatalf("cleaned WebP does not decode: %v", err)
	}
}

func TestStripGIF(t *testing.T) {
	pal := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 4, 4), pal)
	var enc bytes.Buffer
	if err := gif.EncodeAll(&enc, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}, LoopCount: 0}); err != nil {
		t.Fatal(err)
	}
	plain := enc.Bytes()
	gct := 13 // header + logical screen descriptor, then the global colour table if the flag is set
	if plain[10]&0x80 != 0 {
		gct += 3 << (int(plain[10]&0x07) + 1)
	}
	comment := append(append([]byte{0x21, 0xFE, byte(len(secret))}, secret...), 0)
	xmp := append(append(append([]byte{0x21, 0xFF, 11}, "XMP DataXMP"...), byte(len(secret))), append([]byte(secret), 0)...)
	dirty := append(append(append(append([]byte{}, plain[:gct]...), comment...), xmp...), plain[gct:]...)
	p := writeTemp(t, "a.gif", dirty)
	if _, err := stripMetadata(p, "image/gif"); err != nil {
		t.Fatal(err)
	}
	clean, _ := os.ReadFile(p)
	if !bytes.Equal(clean, plain) {
		t.Fatal("GIF not restored to the plain encoder output")
	}
	g, err := gif.DecodeAll(bytes.NewReader(clean))
	if err != nil || len(g.Image) != 2 || g.LoopCount != 0 {
		t.Fatalf("animation broken: %v", err)
	}
}

func TestOrientThumbnail(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255}) // top-left marker
	got := orient(src, 6)                     // rotate 90° clockwise
	if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 3 {
		t.Fatalf("size %v", got.Bounds())
	}
	if r, _, _, _ := got.At(1, 0).RGBA(); r>>8 != 255 {
		t.Fatal("marker should move to the top-right corner")
	}
	if orient(src, 1) != src {
		t.Fatal("orientation 1 must be a no-op")
	}
}
