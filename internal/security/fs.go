package security

import "os"

// OpenReadOnly opens the file at path strictly in read-only mode (os.O_RDONLY).
func OpenReadOnly(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}
