package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReplacesTheFileWithTheGivenMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != "new\n" {
		t.Errorf("file content = %q, want the new content", content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %04o, want 0600", mode)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the file", len(entries))
	}
}

func TestWriteKeepsTheOldFileWhenTheRenameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.conf")

	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	writer := Writer{Rename: func(string, string) error { return errors.New("disk on fire") }}

	err := writer.Write(path, []byte("new\n"), 0o640)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("Write() error = %v, want the rename failure", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != "old\n" {
		t.Errorf("file content = %q, want the old content", content)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want the temporary file removed", len(entries))
	}
}
