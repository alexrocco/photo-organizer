package main

import (
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/alexrocco/photo-organizer/internal/img"
	"golang.org/x/exp/slices"
	"golang.org/x/exp/slog"
)

var imageExts = []string{".JPG", ".ARW"}

func main() {
	sourceDir := os.Args[1]
	validateDir(sourceDir)

	destDir := os.Args[2]
	validateDir(destDir)

	slog.Info("starting", slog.String("source", sourceDir), slog.String("destination", destDir))

	err := filepath.WalkDir(sourceDir, func(p string, entry fs.DirEntry, _ error) error {
		if entry.IsDir() {
			slog.Info("path is a dir, skipping", slog.String("path", p))
			return nil
		}

		fileExt := path.Ext(entry.Name())

		if !slices.Contains(imageExts, strings.ToUpper(fileExt)) {
			slog.Warn("file extension not an image, skipping", slog.String("extension", fileExt))
			return nil
		}

		imgContent, err := ioutil.ReadFile(p)
		if err != nil {
			return fmt.Errorf("error opening image %s: %v", p, err)
		}

		imgExif, err := img.ExtractExif(imgContent)
		if err != nil {
			return fmt.Errorf("error extracting image EXIF: %v", err)
		}

		slog.Info("image EXIF", slog.String("model", imgExif.Model), slog.Any("date", imgExif.TimeDate))

		year := imgExif.TimeDate.Year()
		month := fmt.Sprintf("%02d", imgExif.TimeDate.Month())
		imgDestDir := fmt.Sprintf("%d/%s", year, month)

		slog.Info("final destination", slog.String("path", imgDestDir))

		imgName := fmt.Sprintf("%s-%s%s",
			imgExif.TimeDate.Format("2006-01-02-030405"),
			strings.ToLower(strings.Trim(imgExif.Model, "\"")),
			strings.ToLower(path.Ext(entry.Name())),
		)

		fileDestDir := fmt.Sprintf("%s/%s", destDir, imgDestDir)
		err = os.MkdirAll(fileDestDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("error crating the dir %s: %v", fileDestDir, err)
		}

		imgPath := fmt.Sprintf("%s/%s", fileDestDir, imgName)
		err = ioutil.WriteFile(imgPath, imgContent, 0777)
		if err != nil {
			return fmt.Errorf("error copying file %s: %v", fileDestDir, err)
		}

		slog.Info("image copied", slog.String("path", imgPath))

		return nil
	})
	if err != nil {
		log.Fatalf("\n %v", err)
	}

}

// validateDir validates if the dirPath is really a dir
func validateDir(dirPath string) {
	fileInfo, err := os.Stat(dirPath)
	if err != nil {
		log.Printf("path %s not found", dirPath)
		os.Exit(1)
	}

	if !fileInfo.IsDir() {
		log.Printf("path %s not a dir", dirPath)
		os.Exit(1)
	}
}
