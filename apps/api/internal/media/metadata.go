package media

// Privacy metadata removal (P8-A, ADR-0008). Photos often carry EXIF GPS coordinates, camera serial numbers,
// XMP/IPTC author data or editing history. They are removed at upload time WITHOUT re-encoding: only
// metadata containers are dropped, the compressed image data is copied byte for byte, so the picture
// itself is unchanged (PRD MEDIA-07). The upload UI tells the uploader this happens.
//
//   JPEG: APP1 (EXIF / XMP), APP13 (Photoshop IRB / IPTC), APP3–APP13 vendor blocks and COM are dropped;
//         APP0 (JFIF), APP2 (ICC colour profile) and APP14 (Adobe colour transform) are kept. A non-default
//         EXIF orientation is re-emitted as a minimal EXIF block holding only that tag, so the photo is
//         still displayed upright.
//   PNG:  tEXt / zTXt / iTXt / eXIf / tIME chunks are dropped.
//   WebP: EXIF and XMP chunks are dropped and the VP8X flags / RIFF size are fixed up.
//   GIF:  comment extensions and application extensions other than the animation loop are dropped.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

var errMalformed = errors.New("malformed image container")

// stripMetadata rewrites path in place (via a sibling temp file) without privacy metadata. It returns the
// EXIF orientation found in a JPEG (1 when absent) so thumbnails can be rotated the same way.
func stripMetadata(path, mime string) (orientation int, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 1, err
	}
	var out []byte
	orientation = 1
	switch mime {
	case "image/jpeg":
		out, orientation, err = stripJPEG(src)
	case "image/png":
		out, err = stripPNG(src)
	case "image/webp":
		out, err = stripWebP(src)
	case "image/gif":
		out, err = stripGIF(src)
	default:
		return 1, nil
	}
	if err != nil {
		return 1, err
	}
	if bytes.Equal(out, src) {
		return orientation, nil
	}
	tmp := path + ".clean"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return 1, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 1, err
	}
	return orientation, nil
}

// ---- JPEG ----

func stripJPEG(b []byte) ([]byte, int, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, 1, errMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(b)))
	out.Write(b[:2]) // SOI
	orientation := 1
	i := 2
	for {
		if i+4 > len(b) || b[i] != 0xFF {
			return nil, 1, errMalformed
		}
		marker := b[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xDA { // SOS: the entropy-coded data and everything after are copied verbatim
			if orientation != 1 {
				out.Write(orientationAPP1(orientation))
			}
			out.Write(b[i:])
			return out.Bytes(), orientation, nil
		}
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			return nil, 1, errMalformed // no stand-alone markers are expected before SOS
		}
		length := int(binary.BigEndian.Uint16(b[i+2:]))
		if length < 2 || i+2+length > len(b) {
			return nil, 1, errMalformed
		}
		seg := b[i : i+2+length]
		payload := seg[4:]
		keep := true
		switch {
		case marker == 0xE1: // APP1: EXIF or XMP
			if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
				if o := exifOrientation(payload[6:]); o >= 2 && o <= 8 {
					orientation = o
				}
			}
			keep = false
		case marker >= 0xE3 && marker <= 0xED: // APP3–APP13 (APP13 = Photoshop/IPTC)
			keep = false
		case marker == 0xEF || marker == 0xFE: // APP15, COM
			keep = false
		}
		if keep {
			out.Write(seg)
		}
		i += 2 + length
	}
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF structure; 0 when absent or unreadable.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 {
			return int(bo.Uint16(t[e+8:]))
		}
	}
	return 0
}

// orientationAPP1 builds a minimal big-endian EXIF block whose IFD0 holds only the Orientation tag.
func orientationAPP1(o int) []byte {
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // header, IFD0 at offset 8
		0x00, 0x01, // one entry
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, byte(o >> 8), byte(o), 0x00, 0x00, // Orientation SHORT
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// ---- PNG ----

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

func stripPNG(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, pngSignature) {
		return nil, errMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(b)))
	out.Write(pngSignature)
	for i := len(pngSignature); i < len(b); {
		if i+12 > len(b) {
			return nil, errMalformed
		}
		n := int(binary.BigEndian.Uint32(b[i:]))
		end := i + 12 + n
		if n < 0 || end > len(b) {
			return nil, errMalformed
		}
		switch string(b[i+4 : i+8]) {
		case "tEXt", "zTXt", "iTXt", "eXIf", "tIME":
		default:
			out.Write(b[i:end]) // length, type, data and CRC copied unchanged
		}
		if string(b[i+4:i+8]) == "IEND" {
			return out.Bytes(), nil
		}
		i = end
	}
	return nil, errMalformed
}

// ---- WebP ----

func stripWebP(b []byte) ([]byte, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil, errMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(b)))
	out.Write(b[:12])
	vp8x := -1
	for i := 12; i < len(b); {
		if i+8 > len(b) {
			return nil, errMalformed
		}
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		end := i + 8 + n + n%2 // chunks are padded to an even size
		if n < 0 || i+8+n > len(b) {
			return nil, errMalformed
		}
		if end > len(b) {
			end = len(b)
		}
		switch string(b[i : i+4]) {
		case "EXIF", "XMP ":
		case "VP8X":
			vp8x = out.Len()
			out.Write(b[i:end])
		default:
			out.Write(b[i:end])
		}
		i = end
	}
	res := out.Bytes()
	if vp8x >= 0 && vp8x+8 < len(res) {
		res[vp8x+8] &^= 0x08 | 0x04 // clear the EXIF and XMP flags
	}
	binary.LittleEndian.PutUint32(res[4:], uint32(len(res)-8))
	return res, nil
}

// ---- GIF ----

func stripGIF(b []byte) ([]byte, error) {
	if len(b) < 13 || (string(b[:6]) != "GIF87a" && string(b[:6]) != "GIF89a") {
		return nil, errMalformed
	}
	i := 13
	if b[10]&0x80 != 0 { // global colour table
		i += 3 << (int(b[10]&0x07) + 1)
	}
	if i > len(b) {
		return nil, errMalformed
	}
	out := bytes.NewBuffer(make([]byte, 0, len(b)))
	out.Write(b[:i])
	subBlocks := func(j int) (int, error) {
		for {
			if j >= len(b) {
				return 0, errMalformed
			}
			n := int(b[j])
			j++
			if n == 0 {
				return j, nil
			}
			j += n
		}
	}
	for i < len(b) {
		switch b[i] {
		case 0x3B: // trailer
			out.WriteByte(0x3B)
			return out.Bytes(), nil
		case 0x21: // extension
			if i+2 > len(b) {
				return nil, errMalformed
			}
			label := b[i+1]
			end, err := subBlocks(i + 2)
			if err != nil {
				return nil, err
			}
			keep := true
			switch label {
			case 0xFE: // comment
				keep = false
			case 0xFF: // application extension: keep only the animation loop (NETSCAPE2.0 / ANIMEXTS1.0)
				id := ""
				if i+3+11 <= len(b) && b[i+2] == 11 {
					id = string(b[i+3 : i+3+11])
				}
				keep = id == "NETSCAPE2.0" || id == "ANIMEXTS1.0"
			}
			if keep {
				out.Write(b[i:end])
			}
			i = end
		case 0x2C: // image descriptor (+ local colour table, LZW code size, data sub-blocks)
			if i+10 > len(b) {
				return nil, errMalformed
			}
			j := i + 10
			if b[i+9]&0x80 != 0 {
				j += 3 << (int(b[i+9]&0x07) + 1)
			}
			j++ // LZW minimum code size
			end, err := subBlocks(j)
			if err != nil {
				return nil, err
			}
			out.Write(b[i:end])
			i = end
		default:
			return nil, errMalformed
		}
	}
	return nil, io.ErrUnexpectedEOF
}
