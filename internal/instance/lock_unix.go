//go:build unix

package instance

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes a non-blocking exclusive flock on f.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}
