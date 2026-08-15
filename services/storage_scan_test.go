package services

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestScanUploadFlagsEicarSignature(t *testing.T) {
	path := writeTemp(t, []byte("some content\n"+eicarSignature+"\nmore content"))
	if err := scanUpload(path, int64(len("some content\n"+eicarSignature+"\nmore content"))); err != ErrSuspiciousContent {
		t.Fatalf("scanUpload = %v, want ErrSuspiciousContent", err)
	}
}

func TestScanUploadAllowsPlainContent(t *testing.T) {
	data := []byte("just a normal delivery note")
	path := writeTemp(t, data)
	if err := scanUpload(path, int64(len(data))); err != nil {
		t.Fatalf("scanUpload = %v, want nil", err)
	}
}

func TestScanUploadFlagsExecutableInZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("payload.exe")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte("MZ fake pe header")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	path := writeTemp(t, buf.Bytes())
	if err := scanUpload(path, int64(buf.Len())); err != ErrSuspiciousContent {
		t.Fatalf("scanUpload = %v, want ErrSuspiciousContent", err)
	}
}

func TestScanUploadAllowsCleanZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("readme.txt")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	path := writeTemp(t, buf.Bytes())
	if err := scanUpload(path, int64(buf.Len())); err != nil {
		t.Fatalf("scanUpload = %v, want nil", err)
	}
}
