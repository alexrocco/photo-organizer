package img

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

var dateTimeFormat = "2006:01:02 15:04:05"

type Exif struct {
	Model    string
	TimeDate time.Time
}

// ExtractExif decodes the Model and original date/time from the EXIF metadata
// read from r. r may be a bounded reader over just the file header; goexif only
// consumes what it needs to reach those tags.
func ExtractExif(r io.Reader) (Exif, error) {
	exifF, err := exif.Decode(r)
	if err != nil {
		return Exif{}, fmt.Errorf("error decoding image: %v", err)
	}

	model, err := exifF.Get(exif.Model)
	if err != nil {
		return Exif{}, fmt.Errorf("error getting model from EXIF: %v", err)
	}

	dateTime, err := exifF.Get(exif.DateTimeOriginal)
	if err != nil {
		return Exif{}, fmt.Errorf("error getting date and time from EXIF: %v", err)
	}

	date, err := time.Parse(dateTimeFormat, strings.Trim(dateTime.String(), "\""))
	if err != nil {
		return Exif{}, fmt.Errorf("error formating date: %v", err)
	}

	// Leave only a-z, A-Z, and 0-9 chars
	modelFormatted := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(model.String(), "")
	modelFormatted = strings.Trim(modelFormatted, "\"")

	return Exif{
		Model:    modelFormatted,
		TimeDate: date,
	}, nil
}
