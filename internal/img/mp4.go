package img

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"time"
)

// mp4Epoch is the zero point of MP4 timestamps (seconds since 1904-01-01).
var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// ExtractGoProMP4 reads the capture time and camera model from a GoPro MP4.
//
// The time comes from the movie header (moov/mvhd creation_time). GoPro writes
// the camera's local wall-clock time there, which matches how photo EXIF dates
// are handled. The model comes from the GoPro GPMF metadata in moov/udta (the
// MINF key, e.g. "HERO11 Black"); MP4s without it are rejected as non-GoPro.
//
// GoPro puts the moov box after the (multi-GB) mdat, so r is walked box by
// box with seeks; only the moov box is read into memory.
func ExtractGoProMP4(r io.ReadSeeker) (Exif, error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return Exif{}, fmt.Errorf("error seeking MP4: %v", err)
	}

	moov, err := findBox(r, 0, end, "moov")
	if err != nil {
		return Exif{}, err
	}

	mvhd, ok := childBox(moov, "mvhd")
	if !ok {
		return Exif{}, fmt.Errorf("MP4 has no mvhd box")
	}
	date, err := mvhdCreationTime(mvhd)
	if err != nil {
		return Exif{}, err
	}

	udta, ok := childBox(moov, "udta")
	if !ok {
		return Exif{}, fmt.Errorf("MP4 has no udta box, not a GoPro video")
	}
	gpmf, ok := childBox(udta, "GPMF")
	if !ok {
		return Exif{}, fmt.Errorf("MP4 has no GPMF metadata, not a GoPro video")
	}
	model, ok := gpmfString(gpmf, "MINF")
	if !ok || model == "" {
		return Exif{}, fmt.Errorf("GoPro MP4 has no camera model (GPMF MINF)")
	}

	// Leave only a-z, A-Z, and 0-9 chars
	modelFormatted := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(model, "")

	return Exif{
		Model:    modelFormatted,
		TimeDate: date,
	}, nil
}

// findBox scans the boxes in [start, end) of r and returns the payload of the
// first one of type typ.
func findBox(r io.ReadSeeker, start, end int64, typ string) ([]byte, error) {
	for off := start; off+8 <= end; {
		if _, err := r.Seek(off, io.SeekStart); err != nil {
			return nil, fmt.Errorf("error seeking MP4: %v", err)
		}
		var hdr [16]byte
		if _, err := io.ReadFull(r, hdr[:8]); err != nil {
			return nil, fmt.Errorf("error reading MP4 box header: %v", err)
		}

		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		hdrLen := int64(8)
		switch size {
		case 0: // box runs to the end of the file
			size = end - off
		case 1: // 64-bit size follows the type
			if _, err := io.ReadFull(r, hdr[8:16]); err != nil {
				return nil, fmt.Errorf("error reading MP4 box header: %v", err)
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hdrLen = 16
		}
		if size < hdrLen || off+size > end {
			return nil, fmt.Errorf("corrupt MP4 box %q at offset %d", hdr[4:8], off)
		}

		if string(hdr[4:8]) == typ {
			payload := make([]byte, size-hdrLen)
			if _, err := io.ReadFull(r, payload); err != nil {
				return nil, fmt.Errorf("error reading MP4 %s box: %v", typ, err)
			}
			return payload, nil
		}
		off += size
	}
	return nil, fmt.Errorf("MP4 has no %s box", typ)
}

// childBox returns the payload of the first box of type typ inside an
// already-read container payload.
func childBox(container []byte, typ string) ([]byte, bool) {
	payload, err := findBox(bytes.NewReader(container), 0, int64(len(container)), typ)
	return payload, err == nil
}

// mvhdCreationTime decodes creation_time from an mvhd payload (version 0 has
// 32-bit times, version 1 has 64-bit times, both after 4 bytes of version/flags).
func mvhdCreationTime(mvhd []byte) (time.Time, error) {
	var secs uint64
	switch {
	case len(mvhd) >= 8 && mvhd[0] == 0:
		secs = uint64(binary.BigEndian.Uint32(mvhd[4:8]))
	case len(mvhd) >= 12 && mvhd[0] == 1:
		secs = binary.BigEndian.Uint64(mvhd[4:12])
	default:
		return time.Time{}, fmt.Errorf("unsupported or truncated mvhd box")
	}
	if secs == 0 {
		return time.Time{}, fmt.Errorf("MP4 has no creation time")
	}
	return mp4Epoch.Add(time.Duration(secs) * time.Second), nil
}

// gpmfString finds the first string (type 'c') entry with the given key in a
// GPMF KLV stream, descending into nested (type 0) entries.
//
// Each GPMF entry is a 4-byte key, 1-byte type, 1-byte struct size and 2-byte
// repeat count, followed by size*repeat bytes of data padded to 4 bytes.
func gpmfString(data []byte, key string) (string, bool) {
	for off := 0; off+8 <= len(data); {
		k := string(data[off : off+4])
		typ := data[off+4]
		n := int(data[off+5]) * int(binary.BigEndian.Uint16(data[off+6:off+8]))
		start := off + 8
		if start+n > len(data) {
			return "", false
		}
		value := data[start : start+n]

		if k == key && typ == 'c' {
			return string(bytes.TrimRight(value, "\x00 ")), true
		}
		if typ == 0 {
			if s, ok := gpmfString(value, key); ok {
				return s, true
			}
		}
		off = start + (n+3)&^3
	}
	return "", false
}
