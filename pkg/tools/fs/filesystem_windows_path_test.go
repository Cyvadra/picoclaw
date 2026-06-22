package fstools

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxFsReadDirAcceptsOSSpecificRelativePaths(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "aaa", "bbb", "ccc"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "aaa", "bbb", "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("create nested file: %v", err)
	}

	fsys := &sandboxFs{workspace: workspace}
	entries, err := fsys.ReadDir(filepath.Join("aaa", "bbb"))
	if err != nil {
		t.Fatalf("ReadDir with OS-specific relative path returned error: %v", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name()] = true
	}
	if !seen["ccc"] || !seen["file.txt"] {
		t.Fatalf("ReadDir entries = %v, want ccc and file.txt", seen)
	}
}

func TestSandboxFsOpenAcceptsOSSpecificRelativePaths(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "aaa", "bbb"), 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "aaa", "bbb", "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("create nested file: %v", err)
	}

	fsys := &sandboxFs{workspace: workspace}
	file, err := fsys.Open(filepath.Join("aaa", "bbb", "file.txt"))
	if err != nil {
		t.Fatalf("Open with OS-specific relative path returned error: %v", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q, want %q", string(data), "hello")
	}
}
