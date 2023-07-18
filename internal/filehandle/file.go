package filehandle

import (
	"bytes"
	"fmt"
	"os"
)

// DirExists validates if the dirPath is really a directory
func DirExists(dirPath string) (bool, error) {
	fileInfo, err := os.Stat(dirPath)
	if err != nil {
		return false, fmt.Errorf("path %s not found: %v", dirPath, err)
	}

	return fileInfo.IsDir(), nil
}

// FileExists checks if the file exists in the file system.
func FileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}

	return !info.IsDir()
}

// SameContent checks if the files have the same content.
func SameContent(fileA string, fileB string) (bool, error) {
	contentA, err := os.ReadFile(fileA)
	if err != nil {
		return false, fmt.Errorf("error reading the file %s: %v", fileA, err)
	}

	contentB, err := os.ReadFile(fileB)
	if err != nil {
		return false, fmt.Errorf("error reading the file %s: %v", fileB, err)
	}

	return bytes.Equal(contentA, contentB), nil
}
