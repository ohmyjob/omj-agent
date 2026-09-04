package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A crash between the temporary write and the rename must leave the previous
// file untouched, so the data goes to a sibling file first and only an
// fsynced, complete file is renamed over the destination.
func writeFileAtomically(path string, data []byte, mode os.FileMode, rename func(oldpath, newpath string) error) (err error) {
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
