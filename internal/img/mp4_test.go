package img

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

func gpmfEntry(key string, typ byte, value []byte) []byte {
	b := make([]byte, 8)
	copy(b, key)
	b[4] = typ
	b[5] = 1
	binary.BigEndian.PutUint16(b[6:], uint16(len(value)))
	b = append(b, value...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func mvhdV0(t time.Time) []byte {
	p := make([]byte, 100)
	binary.BigEndian.PutUint32(p[4:], uint32(t.Sub(mp4Epoch)/time.Second))
	return box("mvhd", p)
}

// goProMP4 lays out boxes the way a GoPro does: ftyp, a large mdat, then moov
// at the end with the model in udta/GPMF behind other udta boxes.
func goProMP4(moovChildren ...[]byte) []byte {
	return bytes.Join([][]byte{
		box("ftyp", []byte("mp41\x20\x13\x10\x18mp41")),
		box("free"),
		box("mdat", make([]byte, 4096)),
		box("moov", moovChildren...),
	}, nil)
}

func TestExtractGoProMP4(t *testing.T) {
	want := time.Date(2024, 2, 1, 1, 0, 32, 0, time.UTC)
	gpmf := bytes.Join([][]byte{
		gpmfEntry("FIRM", 'c', []byte("H22.01.02.10.00")),
		gpmfEntry("DEVC", 0, gpmfEntry("MINF", 'c', []byte("HERO11 Black\x00\x00\x00"))),
	}, nil)
	f := goProMP4(
		mvhdV0(want),
		box("udta",
			box("FIRM", []byte("H22.01.02.10.00")),
			box("GPMF", gpmf),
		),
		box("trak", make([]byte, 64)),
	)

	got, err := ExtractGoProMP4(bytes.NewReader(f))
	if err != nil {
		t.Fatalf("ExtractGoProMP4: %v", err)
	}
	if got.Model != "HERO11Black" {
		t.Errorf("model = %q, want %q", got.Model, "HERO11Black")
	}
	if !got.TimeDate.Equal(want) {
		t.Errorf("date = %v, want %v", got.TimeDate, want)
	}
}

func TestExtractGoProMP4LargeMdat(t *testing.T) {
	// A 64-bit (size == 1) mdat header must be skipped correctly.
	mdat := make([]byte, 16+128)
	binary.BigEndian.PutUint32(mdat, 1)
	copy(mdat[4:], "mdat")
	binary.BigEndian.PutUint64(mdat[8:], uint64(len(mdat)))

	want := time.Date(2026, 11, 4, 5, 3, 18, 0, time.UTC)
	f := bytes.Join([][]byte{
		box("ftyp", []byte("mp41")),
		mdat,
		box("moov", mvhdV0(want), box("udta", box("GPMF", gpmfEntry("MINF", 'c', []byte("HERO11 Black"))))),
	}, nil)

	got, err := ExtractGoProMP4(bytes.NewReader(f))
	if err != nil {
		t.Fatalf("ExtractGoProMP4: %v", err)
	}
	if !got.TimeDate.Equal(want) {
		t.Errorf("date = %v, want %v", got.TimeDate, want)
	}
}

func TestExtractGoProMP4RejectsNonGoPro(t *testing.T) {
	f := goProMP4(mvhdV0(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)), box("trak"))

	_, err := ExtractGoProMP4(bytes.NewReader(f))
	if err == nil || !strings.Contains(err.Error(), "not a GoPro") {
		t.Fatalf("err = %v, want a not-a-GoPro error", err)
	}
}
