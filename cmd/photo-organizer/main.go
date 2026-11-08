package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
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

// videoExts are imported too when -video-dest-dir is set, but their metadata is
// read from the MP4 container instead of EXIF. Only GoPro MP4s are supported;
// the low-res .LRV proxies and .THM thumbnails GoPro writes next to them are
// skipped. Unlike images, videos keep their original camera file name and go
// flat into the video destination (the existing layout of the video library).
var videoExts = []string{".MP4"}

func isVideo(p string) bool {
	return slices.Contains(videoExts, strings.ToUpper(path.Ext(p)))
}

// exifHeaderBytes is how much of a file's head we read to extract EXIF on the
// fast path. The Model and DateTimeOriginal tags live near the start of both
// JPEG and Sony ARW files; if they happen to sit beyond this window we fall
// back to reading the whole file.
const exifHeaderBytes = 1 << 20 // 1 MiB

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	sourceDirFlag := flag.String("source-dir", "", "source directory to copy from")
	destDirFlag := flag.String("dest-dir", "", "destination directory to copy to")
	videoDestDirFlag := flag.String("video-dest-dir", "", "destination directory for videos, copied flat with their original names (videos are skipped when unset)")
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

	if *videoDestDirFlag != "" {
		if ok, err := filehandle.DirExists(*videoDestDirFlag); !ok || err != nil {
			logger.Error("video destination dir does not exist or has an error", slog.Any("error", err))
			os.Exit(1)
		}
	}

	sourceDir := *sourceDirFlag
	destDir := *destDirFlag
	videoDestDir := *videoDestDirFlag
	numWorkers := *numWorkersFlag
	verifyFull := *verifyFullFlag

	logger.Info("starting", slog.String("source", sourceDir), slog.String("destination", destDir), slog.String("videoDestination", videoDestDir))

	imgPaths := []string{}

	_ = filepath.WalkDir(sourceDir, func(p string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			logger.Info("path is a dir, skipping", slog.String("path", p))
			return nil
		}

		fileExt := path.Ext(entry.Name())

		if !slices.Contains(imageExts, strings.ToUpper(fileExt)) && !isVideo(p) {
			logger.Warn("file extension not an image, skipping", slog.String("extension", fileExt))
			return nil
		}

		if isVideo(p) && videoDestDir == "" {
			logger.Warn("no video destination dir set, skipping video", slog.String("path", p))
			return nil
		}

		imgPaths = append(imgPaths, p)

		return nil
	})

	logger.Info("images found", slog.Int("number", len(imgPaths)))

	jobs := make(chan string, len(imgPaths))
	errors := make(chan error, len(imgPaths))

	for i := 0; i < numWorkers; i++ {
		go func(workerId int, jobs <-chan string, destDir string, videoDestDir string, verifyFull bool, errors chan<- error, logger *slog.Logger) {
			for j := range jobs {
				err := copyImage(j, destDir, videoDestDir, verifyFull, workerId, logger)
				errors <- err
			}
		}(i, jobs, destDir, videoDestDir, verifyFull, errors, logger)
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
// with the standard name and directory structure. Videos instead go flat into
// videoDestDir under their original file name.
//
// The already-imported case is resolved with cheap metadata (EXIF from the file
// header, then a size + head/tail fingerprint comparison), so it does not read
// the whole source or its destination twin. Only a genuinely new image is
// streamed to the destination (never held in memory, since GoPro videos are
// multi-GB) and verified by checksum. With verifyFull set, the dedup comparison
// falls back to comparing both files in full.
func copyImage(origImgPath string, destDir string, videoDestDir string, verifyFull bool, workerId int, logger *slog.Logger) error {
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

	var fileDestDir string
	var slotName func(fileCounter int) string

	if isVideo(origImgPath) {
		// Keep the camera's name (GX010182.MP4). Cameras reuse names (counter
		// resets, several cameras), so a different clip already holding the name
		// gets a counter suffix: GX010182-01.MP4.
		fileDestDir = videoDestDir
		origName := path.Base(origImgPath)
		ext := path.Ext(origName)
		stem := strings.TrimSuffix(origName, ext)
		slotName = func(fileCounter int) string {
			if fileCounter == 0 {
				return origName
			}
			return fmt.Sprintf("%s-%02d%s", stem, fileCounter, ext)
		}
	} else {
		year := imgExif.TimeDate.Year()
		month := fmt.Sprintf("%02d", imgExif.TimeDate.Month())
		fileDestDir = fmt.Sprintf("%s/%d/%s", destDir, year, month)

		baseName := imgExif.TimeDate.Format("2006-01-02-030405")
		model := strings.ToLower(imgExif.Model)
		ext := strings.ToLower(path.Ext(origImgPath))
		slotName = func(fileCounter int) string {
			return fmt.Sprintf("%s-%s-%02d%s", baseName, model, fileCounter, ext)
		}
	}

	logger.Info("final destination",
		slog.String("path", fileDestDir),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	err = os.MkdirAll(fileDestDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error crating the dir %s: %v", fileDestDir, err)
	}

	// Walk the counter-suffixed names. An existing slot holding the same file
	// means it is already imported (skip); an existing slot holding a different
	// file (same timestamp+model, e.g. a burst) means try the next counter; the
	// first free slot is where a new image gets written.
	for fileCounter := 0; ; fileCounter++ {
		imgName := slotName(fileCounter)
		copyImgPath := fmt.Sprintf("%s/%s", fileDestDir, imgName)

		// Claim the slot atomically. O_EXCL guarantees exactly one worker
		// creates a given name, so two workers can never both take the same
		// counter for different same-second frames (which previously raced and
		// lost files via the delete-on-mismatch path below).
		dst, err := os.OpenFile(copyImgPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.ModePerm)
		if os.IsExist(err) {
			// Slot already taken (another worker or a previous run). Same file?
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
			continue // a different image holds this slot, try the next counter
		}
		if err != nil {
			return fmt.Errorf("error creating %s: %v", copyImgPath, err)
		}

		// We exclusively own a fresh slot: stream the source in, hashing it on
		// the way through.
		src, err := os.Open(origImgPath)
		if err != nil {
			dst.Close()
			os.Remove(copyImgPath)
			return fmt.Errorf("error opening image %s: %v", origImgPath, err)
		}
		srcHash := sha256.New()
		_, err = io.Copy(io.MultiWriter(dst, srcHash), src)
		src.Close()
		if err != nil {
			dst.Close()
			os.Remove(copyImgPath)
			return fmt.Errorf("error copying file %s: %v", copyImgPath, err)
		}
		if err := dst.Close(); err != nil {
			os.Remove(copyImgPath)
			return fmt.Errorf("error closing %s: %v", copyImgPath, err)
		}

		// Verify what landed matches the source. Deleting on mismatch is safe
		// now: we are the sole owner of this path, so no other worker's file
		// can be removed here.
		dstSum, err := filehandle.Hash(copyImgPath)
		if err != nil {
			return fmt.Errorf("error reading image just written %s: %v", copyImgPath, err)
		}
		if !bytes.Equal(dstSum, srcHash.Sum(nil)) {
			logger.Warn("deleting image as the content is not equal",
				slog.String("path", copyImgPath),
				slog.Int("workerId", workerId),
				slog.String("origImgPath", origImgPath),
			)
			if err := os.Remove(copyImgPath); err != nil {
				return fmt.Errorf("error deleting image not copied correctly %s: %v", copyImgPath, err)
			}
			return fmt.Errorf("written image %s did not match source, removed", copyImgPath)
		}

		logger.Info("image copied",
			slog.String("path", copyImgPath),
			slog.Int("workerId", workerId),
			slog.String("origImgPath", origImgPath),
		)
		return nil
	}
}

// extractExif reads EXIF from just the head of the file (exifHeaderBytes) to
// avoid loading whole multi-MB images, falling back to the full file if the
// tags we need sit past that window. Videos have no EXIF; their equivalent
// metadata is read from the MP4 container.
func extractExif(imgPath string) (img.Exif, error) {
	if slices.Contains(videoExts, strings.ToUpper(path.Ext(imgPath))) {
		f, err := os.Open(imgPath)
		if err != nil {
			return img.Exif{}, fmt.Errorf("error opening video %s: %v", imgPath, err)
		}
		defer f.Close()
		return img.ExtractGoProMP4(f)
	}

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
