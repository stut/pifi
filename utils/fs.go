package utils

import (
	"fmt"
	"os"
)

// PathExists checks if a path exists
func PathExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MoveFile moves a file from src to dst
func MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("moving file %s to %s: %w", src, dst, err)
	}
	return nil
}

// DeleteFile deletes a file
func DeleteFile(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("deleting file %s: %w", path, err)
	}
	return nil
}
