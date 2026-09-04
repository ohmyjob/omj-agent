// Package atomicfile replaces files so that a crash never leaves a partial one behind.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Writer writes the data to a sibling temporary file, fsyncs it and renames
// it over the destination, so a crash at any point leaves either the previous
// file or the complete new one. Rename is injectable so a test can prove that.
type Writer struct {
	Rename func(oldpath, newpath string) error
}

func Write(path string, data []byte, mode os.FileMode) error {
	return Writer{}.Write(path, data, mode)
}

func (w Writer) Write(path string, data []byte, mode os.FileMode) (err error) {
	rename := w.Rename
	if rename == nil {
		rename = os.Rename
	}

	dir, base := filepath.Split(path)

	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(tmp.Name()))
		}
	}()

	if err = tmp.Chmod(mode); err != nil {
		return errors.Join(fmt.Errorf("set mode: %w", err), tmp.Close())
	}

	if _, err = tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write: %w", err), tmp.Close())
	}

	if err = tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync: %w", err), tmp.Close())
	}

	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	if err = rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}

	return nil
}
