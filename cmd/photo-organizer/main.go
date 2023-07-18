package main

import (
	"fmt"
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

func main() {
	sourceDir := os.Args[1]
	if ok, err := filehandle.DirExists(sourceDir); !ok || err != nil {
		slog.Error("source dir does not exist or has an error", slog.Any("error", err))
		os.Exit(1)
	}

	destDir := os.Args[2]
	if ok, err := filehandle.DirExists(destDir); !ok || err != nil {
		slog.Error("destination dir does not exist or has an error", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("starting", slog.String("source", sourceDir), slog.String("destination", destDir))

	imgPaths := []string{}

	_ = filepath.WalkDir(sourceDir, func(p string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			slog.Info("path is a dir, skipping", slog.String("path", p))
			return nil
		}

		fileExt := path.Ext(entry.Name())

		if !slices.Contains(imageExts, strings.ToUpper(fileExt)) {
			slog.Warn("file extension not an image, skipping", slog.String("extension", fileExt))
			return nil
		}

		imgPaths = append(imgPaths, p)

		return nil
	})

	numWorkers := 8

	jobs := make(chan string, len(imgPaths))
	errors := make(chan error, len(imgPaths))

	for i := 0; i < numWorkers; i++ {
		go func(workerId int, jobs <-chan string, destDir string, errors chan<- error) {
			for j := range jobs {
				err := copyImage(j, destDir, workerId)
				errors <- err
			}
		}(i, jobs, destDir, errors)
	}

	for j := 0; j < len(imgPaths); j++ {
		jobs <- imgPaths[j]
	}
	close(jobs)

	for a := 1; a <= len(imgPaths); a++ {
		err := <-errors
		if err != nil {
			slog.Error(fmt.Sprintf("error msg: %v", err))
		}
	}

}

// copyImage copy the image to the destination directory,
// with the standard name and directory structure.
func copyImage(origImgPath string, destDir string, workerId int) error {
	imgContent, err := os.ReadFile(origImgPath)
	if err != nil {
		return fmt.Errorf("error opening image %s: %v", origImgPath, err)
	}

	imgExif, err := img.ExtractExif(imgContent)
	if err != nil {
		return fmt.Errorf("error extracting image EXIF: %v", err)
	}

	slog.Info("image EXIF",
		slog.String("model", imgExif.Model),
		slog.Any("date", imgExif.TimeDate),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	year := imgExif.TimeDate.Year()
	month := fmt.Sprintf("%02d", imgExif.TimeDate.Month())
	imgDestDir := fmt.Sprintf("%d/%s", year, month)

	slog.Info("final destination",
		slog.String("path", imgDestDir),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	fileDestDir := fmt.Sprintf("%s/%s", destDir, imgDestDir)
	err = os.MkdirAll(fileDestDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error crating the dir %s: %v", fileDestDir, err)
	}

	// starts with negative as the loop will bump it
	fileCounter := -1
	var imgName, copyImgPath string
	// sets that the file exists to start the loop
	imgExists := true
	// flag that checks if the image already exists with different name
	// this is useful when running this same
	hasSameContent := false
	for imgExists && !hasSameContent {
		fileCounter++
		imgName = fmt.Sprintf("%s-%s-%02d%s",
			imgExif.TimeDate.Format("2006-01-02-030405"),
			strings.ToLower(imgExif.Model),
			fileCounter,
			strings.ToLower(path.Ext(origImgPath)),
		)

		copyImgPath = fmt.Sprintf("%s/%s", fileDestDir, imgName)

		imgExists = filehandle.FileExists(copyImgPath)

		if imgExists {
			hasSameContent, err = filehandle.SameContent(copyImgPath, origImgPath)
			if err != nil {
				return fmt.Errorf("error comparing files: %s with %s: %v", copyImgPath, origImgPath, err)
			}
		}
	}

	if hasSameContent {
		slog.Warn("image skipped as already exists",
			slog.String("path", copyImgPath),
			slog.Int("workerId", workerId),
			slog.String("origImgPath", origImgPath),
		)

		return nil
	}

	err = os.WriteFile(copyImgPath, imgContent, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error copying file %s: %v", fileDestDir, err)
	}

	slog.Info("image copied",
		slog.String("path", copyImgPath),
		slog.Int("workerId", workerId),
		slog.String("origImgPath", origImgPath),
	)

	return nil
}
