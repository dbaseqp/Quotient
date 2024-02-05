package checks

import (
	"crypto/sha256"
	"encoding/hex"
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

func StringHash(fileContent string) (string, error) {
	hasher := sha256.New()
	_, err := hasher.Write([]byte(fileContent))
	if err != nil {
		return "", err
	}
	return hexEncode(string(hasher.Sum(nil))), nil
}

func GetFile(fileName string) (string, error) {
	// TODO: fix insecure file path handling
	// this isn't really an issue since if you can
	// edit the config, you already have as shell,
	// but whatever. and it's only reading/hashing
	fileContent, err := os.ReadFile("./scoredfiles/" + fileName)
	if err != nil {
		return "", err
	}
	return string(fileContent), nil
}

func hexEncode(inputString string) string {
	return hex.EncodeToString([]byte(inputString))
}
