package checks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"

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

// RunSubchecks abstracts the pattern of checking either a single random subcheck
// or all subchecks (stopping at the first failure).
// An optional debugSuffix (like credentials used) will be appended to the final debug string.
func RunSubchecks[T any](items []T, checkAll bool, baseResult Result, debugSuffix string, checkFn func(item T, result Result) Result) Result {
	if len(items) == 0 {
		baseResult.Status = true
		if debugSuffix != "" {
			baseResult.Debug = debugSuffix
		}
		return baseResult
	}

	if checkAll {
		debugParts := []string{}
		for _, item := range items {
			result := checkFn(item, baseResult)
			if !result.Status {
				if debugSuffix != "" {
					if result.Debug == "" {
						result.Debug = debugSuffix
					} else {
						result.Debug += " (" + debugSuffix + ")"
					}
				}
				return result
			}
			if result.Debug != "" {
				debugParts = append(debugParts, result.Debug)
			}
		}
		baseResult.Status = true
		
		if len(debugParts) > 0 {
			baseResult.Debug = fmt.Sprintf("all %d checks passed: %s", len(items), strings.Join(debugParts, "; "))
		} else {
			baseResult.Debug = fmt.Sprintf("all %d checks passed", len(items))
		}

		if debugSuffix != "" {
			baseResult.Debug += " (" + debugSuffix + ")"
		}
		return baseResult
	} else {
		item := items[rand.Intn(len(items))] // #nosec G404 -- non-crypto random selection
		res := checkFn(item, baseResult)
		if debugSuffix != "" {
			if res.Debug == "" {
				res.Debug = debugSuffix
			} else {
				res.Debug += " (" + debugSuffix + ")"
			}
		}
		return res
	}
}
