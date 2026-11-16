package atomicfile

import (
	"errors"
	"syscall"
)

// isUnsupported reports whether fsync failed only because the filesystem does
// not implement it for directories.
func isUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
