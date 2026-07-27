//go:build !windows

package files

import "os"

func isUnsafeDirectoryLink(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
