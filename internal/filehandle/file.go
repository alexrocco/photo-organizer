package filehandle

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// sampleSize is how many bytes are hashed from the head (and tail) of a file
// when fingerprinting it for a cheap same-file comparison.
const sampleSize = 64 << 10 // 64 KiB

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

// SameContent checks if the files have the same content by reading both in
// full. Exhaustive but expensive; used only when full verification is asked for.
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

// SameFile reports whether dst and src are (to a very high certainty) the same
// file, without reading either in full. Files of different size are trivially
// different; files of equal size are compared by a fingerprint over their head
// and tail bytes. This makes the common "already imported" case cost a couple
// of stats plus ~128 KiB instead of two full-file reads.
func SameFile(dst string, src string) (bool, error) {
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return false, fmt.Errorf("error stating %s: %v", dst, err)
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false, fmt.Errorf("error stating %s: %v", src, err)
	}

	if dstInfo.Size() != srcInfo.Size() {
		return false, nil
	}

	dstFp, err := fingerprint(dst)
	if err != nil {
		return false, err
	}
	srcFp, err := fingerprint(src)
	if err != nil {
		return false, err
	}

	return dstFp == srcFp, nil
}

// fingerprint hashes the first and last sampleSize bytes of the file (or the
// whole file when it is smaller than 2*sampleSize).
func fingerprint(filePath string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte

	f, err := os.Open(filePath)
	if err != nil {
		return sum, fmt.Errorf("error opening %s: %v", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return sum, fmt.Errorf("error stating %s: %v", filePath, err)
	}

	h := sha256.New()
	if info.Size() <= 2*sampleSize {
		if _, err := io.Copy(h, f); err != nil {
			return sum, fmt.Errorf("error reading %s: %v", filePath, err)
		}
	} else {
		if _, err := io.CopyN(h, f, sampleSize); err != nil {
			return sum, fmt.Errorf("error reading head of %s: %v", filePath, err)
		}
		if _, err := f.Seek(-sampleSize, io.SeekEnd); err != nil {
			return sum, fmt.Errorf("error seeking tail of %s: %v", filePath, err)
		}
		if _, err := io.CopyN(h, f, sampleSize); err != nil {
			return sum, fmt.Errorf("error reading tail of %s: %v", filePath, err)
		}
	}

	copy(sum[:], h.Sum(nil))
	return sum, nil
}
