package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash/v2"
)

func Fingerprint(path string) (fileType, checksum string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("not a regular file")
	}
	digest := xxhash.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", "", err
	}
	return strings.ToLower(filepath.Ext(path)), fmt.Sprintf("xxh64:%016x", digest.Sum64()), nil
}
