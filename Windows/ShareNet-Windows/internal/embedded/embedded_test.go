package embedded

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZipToDir(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("tools/sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("hello-sharenet"))
	_ = zw.Close()

	dir := t.TempDir()
	if err := ExtractZipToDir(buf.Bytes(), dir); err != nil {
		t.Fatalf("ExtractZipToDir failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "tools", "sample.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "hello-sharenet" {
		t.Fatalf("got %q, want %q", string(got), "hello-sharenet")
	}
}

func TestExtractZipToDirRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../evil.txt")
	_, _ = f.Write([]byte("bad"))
	_ = zw.Close()

	dir := t.TempDir()
	if err := ExtractZipToDir(buf.Bytes(), dir); err == nil {
		t.Fatal("expected error for path traversal entry")
	}
}
