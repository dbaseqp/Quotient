package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/pmezard/go-difflib/difflib"
)

// FileDifference returns the percentage difference
// between the contents of the filename passed and
// the contents of the file passed.
func FileDifference(fileName string, fileContent string) (int, error) {
	originalFileContent, err := GetFile(fileName)
	if err != nil {
		return 0, err
	}
	diffMatcher := difflib.NewMatcher([]string{originalFileContent}, []string{fileContent})
	return int((diffMatcher.Ratio() + 0.5) * 100), nil
}

// FileHash returns the sha256sum of the filename
// passed.
func FileHash(fileName string) (string, error) {
	fileContent, err := GetFile(fileName)
	if err != nil {
		return "", err
	}
	return StringHash(fileContent)
}

// StringHash returns the sha256sum of the string
func StringHash(fileContent string) (string, error) {
	hasher := sha256.New()
	if _, err := hasher.Write([]byte(fileContent)); err != nil {
		return "", err
	}
	// Directly encode the byte slice returned by hasher.Sum to avoid
	// corrupting non-UTF8 bytes when converting to and from strings.
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func GetFile(fileName string) (string, error) {
	root, err := os.OpenRoot("./scoredfiles")
	if err != nil {
		return "", fmt.Errorf("failed to open scoredfiles directory: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			// Non-fatal, just log if available
		}
	}()

	file, err := root.Open(fileName)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Non-fatal, just log if available
		}
	}()

	fileContent, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(fileContent), nil
}
