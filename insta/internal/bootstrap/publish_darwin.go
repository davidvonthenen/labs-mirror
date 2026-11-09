//go:build darwin

package bootstrap

import "golang.org/x/sys/unix"

func publishDirectory(stage, destination string) error {
	return unix.RenamexNp(stage, destination, unix.RENAME_EXCL)
}
