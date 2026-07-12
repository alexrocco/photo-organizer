package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/alexrocco/photo-organizer/internal/filehandle"
	"github.com/alexrocco/photo-organizer/internal/img"
	"golang.org/x/exp/slices"
	"golang.org/x/exp/slog"
)

var imageExts = []string{".JPG", ".ARW"}

// exifHeaderBytes is how much of a file's head we read to extract EXIF on the
// fast path. The Model and DateTimeOriginal tags live near the start of both
// JPEG and Sony ARW files; if they happen to sit beyond this window we fall
// back to reading the whole file.
const exifHeaderBytes = 1 << 20 // 1 MiB

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	sourceDirFlag := flag.String("source-dir", "", "source directory to copy from")
	destDirFlag := flag.String("dest-dir", "", "destination directory to copy to")
	numWorkersFlag := flag.Int("num-workers", 2, "number of parallel workers to copy the images")
	verifyFullFlag := flag.Bool("verify-full", false, "compare full file contents when deduping (slower; default compares size + head/tail fingerprint)")
	flag.Parse()

	if ok, err := filehandle.DirExists(*sourceDirFlag); !ok || err != nil {
		logger.Error("source dir does not exist or has an error", slog.Any("error", err))
		os.Exit(1)
	}

	if ok, err := filehandle.DirExists(*destDirFlag); !ok || err != nil {
		logger.Error("destination dir does not exist or has an error", slog.Any("error", err))
		os.Exit(1)
	}

	sourceDir := *sourceDirFlag
	destDir := *destDirFlag
	numWorkers := *numWorkersFlag
	verifyFull := *verifyFullFlag

	logger.Info("starting", slog.String("source", sourceDir), slog.String("destination", destDir))

	imgPaths := []string{}

	_ = filepath.WalkDir(sourceDir, func(p string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			logger.Info("path is a dir, skipping", slog.String("path", p))
			return nil
		}

		fileExt := path.Ext(entry.Name())

		if !slices.Contains(imageExts, strings.ToUpper(fileExt)) {
			logger.Warn("file extension not an image, skipping", slog.String("extension", fileExt))
			return nil
		}

		imgPaths = append(imgPaths, p)

		return nil
	})

	logger.Info("images found", slog.Int("number", len(imgPaths)))

	jobs := make(chan string, len(imgPaths))
	errors := make(chan error, len(imgPaths))

	for i := 0; i < numWorkers; i++ {
		go func(workerId int, jobs <-chan string, destDir string, verifyFull bool, errors chan<- error, logger *slog.Logger) {
			for j := range jobs {
				err := copyImage(j, destDir, verifyFull, workerId, logger)
				errors <- err
			}
		}(i, jobs, destDir, verifyFull, errors, logger)
	}

	for j := 0; j < len(imgPaths); j++ {
		jobs <- imgPaths[j]
	}
	close(jobs)

	for a := 1; a <= len(imgPaths); a++ {
		err := <-errors
		if err != nil {
			logger.Error(fmt.Sprintf("error msg: %v", err))
		}
	}

}

// copyImage copy the image to the destination directory,
// with the standard name and directory structure.
//
// The already-imported case is resolved with cheap metadata (EXIF from the file
// header, then a size + head/tail fingerprint comparison), so it does not read
// the whole source or its destination twin. Only a genuinely new image is read
// in full and written out. With verifyFull set, the dedup comparison falls back
// to reading both files in full.
func copyImage(origImgPath string, destDir string, verifyFull bool, workerId int, logger *slog.Logger) error {
	imgExif, err := extractExif(origImgPath)
	if err != nil {
		return fmt.Errorf("error extracting image EXIF: %v", err)
	}

	logger.Info("image EXIF",
		slog.String("model", imgExif.Model),
		slog.Any("date", imgExif.TimeDate),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	year := imgExif.TimeDate.Year()
	month := fmt.Sprintf("%02d", imgExif.TimeDate.Month())
	imgDestDir := fmt.Sprintf("%d/%s", year, month)

	logger.Info("final destination",
		slog.String("path", imgDestDir),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	fileDestDir := fmt.Sprintf("%s/%s", destDir, imgDestDir)
	err = os.MkdirAll(fileDestDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error crating the dir %s: %v", fileDestDir, err)
	}

	// Walk the counter-suffixed names. An existing slot holding the same file
	// means it is already imported (skip); an existing slot holding a different
	// file (same timestamp+model, e.g. a burst) means try the next counter; the
	// first free slot is where a new image gets written.
	baseName := imgExif.TimeDate.Format("2006-01-02-030405")
	model := strings.ToLower(imgExif.Model)
	ext := strings.ToLower(path.Ext(origImgPath))

	var copyImgPath string
	for fileCounter := 0; ; fileCounter++ {
		imgName := fmt.Sprintf("%s-%s-%02d%s", baseName, model, fileCounter, ext)
		copyImgPath = fmt.Sprintf("%s/%s", fileDestDir, imgName)

		if !filehandle.FileExists(copyImgPath) {
			break // free slot -> this is a new image, copy it below
		}

		var sameContent bool
		if verifyFull {
			sameContent, err = filehandle.SameContent(copyImgPath, origImgPath)
		} else {
			sameContent, err = filehandle.SameFile(copyImgPath, origImgPath)
		}
		if err != nil {
			return fmt.Errorf("error comparing files: %s with %s: %v", copyImgPath, origImgPath, err)
		}

		if sameContent {
			logger.Warn("image skipped as already exists",
				slog.String("path", copyImgPath),
				slog.Int("workerId", workerId),
				slog.String("origImgPath", origImgPath),
			)
			return nil
		}
	}

	// New image: now read it in full and write it out.
	imgContent, err := os.ReadFile(origImgPath)
	if err != nil {
		return fmt.Errorf("error opening image %s: %v", origImgPath, err)
	}

	err = os.WriteFile(copyImgPath, imgContent, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error copying file %s: %v", fileDestDir, err)
	}

	wImgContent, err := os.ReadFile(copyImgPath)
	if err != nil {
		return fmt.Errorf("error reading image just written %s: %v", copyImgPath, err)
	}

	// Check if the image written has the same content of the origin image
	if !bytes.Equal(wImgContent, imgContent) {
		logger.Warn("deleting image as the content is not equal",
			slog.String("path", copyImgPath),
			slog.Int("workerId", workerId),
			slog.String("origImgPath", origImgPath),
		)
		err = os.Remove(copyImgPath)
		if err != nil {
			return fmt.Errorf("error deleting image not copied correctly %s: %v", copyImgPath, err)
		}
	}

	logger.Info("image copied",
		slog.String("path", copyImgPath),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	return nil
}

// extractExif reads EXIF from just the head of the file (exifHeaderBytes) to
// avoid loading whole multi-MB images, falling back to the full file if the
// tags we need sit past that window.
func extractExif(imgPath string) (img.Exif, error) {
	if e, err := extractExifLimited(imgPath, exifHeaderBytes); err == nil {
		return e, nil
	}
	return extractExifLimited(imgPath, -1)
}

// extractExifLimited extracts EXIF from imgPath, reading at most limit bytes
// (limit < 0 means the whole file).
func extractExifLimited(imgPath string, limit int64) (img.Exif, error) {
	f, err := os.Open(imgPath)
	if err != nil {
		return img.Exif{}, fmt.Errorf("error opening image %s: %v", imgPath, err)
	}
	defer f.Close()

	var r io.Reader = bufio.NewReader(f)
	if limit >= 0 {
		r = io.LimitReader(r, limit)
	}

	return img.ExtractExif(r)
}
