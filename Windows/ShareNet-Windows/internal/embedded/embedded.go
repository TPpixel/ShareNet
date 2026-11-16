package embedded

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed tools.zip
var toolsZip []byte

// EnsureRuntimeRoot extracts bundled tools (adb, gnirehtet, APKs, sing-box) once
// into %LOCALAPPDATA%\ShareNet\runtime\<hash>\ and returns that root directory.
func EnsureRuntimeRoot() (string, error) {
	if len(toolsZip) == 0 {
		return "", fmt.Errorf("embedded tools bundle is empty")
	}
	baseDir := os.Getenv("LOCALAPPDATA")
	if baseDir == "" {
		if d, err := os.UserCacheDir(); err == nil && d != "" {
			baseDir = d
		} else {
			baseDir = os.TempDir()
		}
	}
	runtimeParent := filepath.Join(baseDir, "ShareNet", "runtime")
	sum := sha256.Sum256(toolsZip)
	tag := hex.EncodeToString(sum[:6])
	targetRoot := filepath.Join(runtimeParent, tag)

	if isExtractedReady(targetRoot) {
		return targetRoot, nil
	}

	tmpRoot := targetRoot + ".tmp"
	_ = os.RemoveAll(tmpRoot)
	if err := ExtractZipToDir(toolsZip, tmpRoot); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, ".ready"), []byte(tag), 0644); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return "", err
	}

	_ = os.RemoveAll(targetRoot)
	if err := os.Rename(tmpRoot, targetRoot); err != nil {
		// Fallback copy-free check if another instance raced cleanly
		if isExtractedReady(targetRoot) {
			_ = os.RemoveAll(tmpRoot)
			return targetRoot, nil
		}
		return tmpRoot, nil
	}

	cleanupOldRuntimes(runtimeParent, tag)
	return targetRoot, nil
}

func isExtractedReady(root string) bool {
	required := []string{
		filepath.Join(root, ".ready"),
		filepath.Join(root, "tools", "platform-tools", "adb.exe"),
		filepath.Join(root, "tools", "gnirehtet", "gnirehtet.exe"),
		filepath.Join(root, "tools", "sing-box", "sing-box.exe"),
		filepath.Join(root, "tools", "sing-box", "sing-box-android"),
	}
	for _, p := range required {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return false
		}
	}
	return true
}

func cleanupOldRuntimes(parent, keepTag string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name != keepTag {
			_ = os.RemoveAll(filepath.Join(parent, name))
		}
	}
}

// ExtractZipToDir safely unpacks rawZip into dstDir, rejecting zip-slip traversal paths.
func ExtractZipToDir(rawZip []byte, dstDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(rawZip), int64(len(rawZip)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	cleanBase := filepath.Clean(dstDir)
	if err := os.MkdirAll(cleanBase, 0755); err != nil {
		return err
	}

	for _, f := range zr.File {
		rel := filepath.FromSlash(f.Name)
		dest := filepath.Join(cleanBase, rel)
		if dest != cleanBase && !strings.HasPrefix(dest, cleanBase+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeOutErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}
