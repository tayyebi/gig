package services

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupportedMediaType is returned when an upload's sniffed content type
// is not in the allowed image list.
var ErrUnsupportedMediaType = errors.New("unsupported file type")

// ErrFileTooLarge is returned when an upload exceeds the configured limit.
var ErrFileTooLarge = errors.New("file exceeds the maximum upload size")

// ErrSuspiciousContent is returned when a lightweight signature/extension
// scan flags an upload. This is not malware scanning against a real AV
// engine (TODO.md Phase 3 explicitly defers that, and PLAN.md's hard
// constraints keep the stack dependency-light); it catches the cheap,
// common cases: a classic AV-test signature, and archive members whose
// names carry an executable/script extension.
var ErrSuspiciousContent = errors.New("upload flagged by content scan")

// eicarSignature is the standard antivirus test string
// (https://www.eicar.org/download-anti-malware-testfile/). Any file
// containing it is rejected; it is inert but a reliable, dependency-free
// smoke test that the scan path actually runs.
const eicarSignature = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

// dangerousArchiveExtensions are executable/script extensions that should
// never appear inside an uploaded zip archive (deliveries, portfolio zips).
var dangerousArchiveExtensions = []string{
	".exe", ".dll", ".so", ".dylib", ".bat", ".cmd", ".com", ".scr",
	".ps1", ".sh", ".js", ".jar", ".msi", ".vbs", ".apk",
}

// allowedImageTypes maps sniffed content types to a safe file extension.
// Portfolio and gig media are images only.
var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// allowedOrderFileTypes extends the image set with document and archive
// formats sellers and buyers reasonably need for deliveries and dispute
// evidence. Sniffed from content, not the client-supplied filename.
var allowedOrderFileTypes = map[string]string{
	"image/jpeg":                ".jpg",
	"image/png":                 ".png",
	"image/gif":                 ".gif",
	"image/webp":                ".webp",
	"application/pdf":           ".pdf",
	"application/zip":           ".zip",
	"text/plain; charset=utf-8": ".txt",
}

// Storage saves validated uploads under a root directory on local disk. The
// root is served back to browsers by a plain static file handler, so nothing
// written here is treated as private.
type Storage struct {
	Root     string
	MaxBytes int64
}

// NewStorage returns a Storage rooted at dir, rejecting uploads over maxBytes.
func NewStorage(dir string, maxBytes int64) *Storage {
	return &Storage{Root: dir, MaxBytes: maxBytes}
}

// SaveImage validates and writes an uploaded image under subdir (a
// caller-controlled, non-user-supplied path such as "portfolio/42"),
// returning the slash-separated relative path to persist in the database.
func (st *Storage) SaveImage(subdir string, file multipart.File, header *multipart.FileHeader) (string, error) {
	return st.save(subdir, file, header, allowedImageTypes)
}

// SaveOrderFile validates and writes an uploaded delivery or dispute-evidence
// file under subdir. It accepts a broader set of document and archive types
// than SaveImage since sellers deliver more than images.
func (st *Storage) SaveOrderFile(subdir string, file multipart.File, header *multipart.FileHeader) (string, error) {
	return st.save(subdir, file, header, allowedOrderFileTypes)
}

func (st *Storage) save(subdir string, file multipart.File, header *multipart.FileHeader, allowed map[string]string) (string, error) {
	if header.Size > st.MaxBytes {
		return "", ErrFileTooLarge
	}

	sniff := make([]byte, 512)
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("read upload: %w", err)
	}
	ext, ok := allowed[http.DetectContentType(sniff[:n])]
	if !ok {
		return "", ErrUnsupportedMediaType
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek upload: %w", err)
	}

	name, err := randomFilename(ext)
	if err != nil {
		return "", err
	}
	relPath := filepath.ToSlash(filepath.Join(subdir, name))
	absPath := filepath.Join(st.Root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", fmt.Errorf("create upload directory: %w", err)
	}

	dst, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("create upload file: %w", err)
	}
	written, copyErr := io.Copy(dst, io.LimitReader(file, st.MaxBytes))
	dst.Close()
	if copyErr != nil {
		os.Remove(absPath)
		return "", fmt.Errorf("write upload file: %w", copyErr)
	}

	if err := scanUpload(absPath, written); err != nil {
		os.Remove(absPath)
		return "", err
	}
	return relPath, nil
}

// scanUpload runs the lightweight signature/extension checks described on
// ErrSuspiciousContent against the file just written to disk. It re-reads
// the file (bounded by MaxBytes, already enforced above) rather than the
// original multipart reader, since the zip check needs random access that
// an io.Reader over the wire does not offer.
func scanUpload(absPath string, size int64) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read upload for scan: %w", err)
	}
	if bytes.Contains(data, []byte(eicarSignature)) {
		return ErrSuspiciousContent
	}
	if zr, err := zip.NewReader(bytes.NewReader(data), size); err == nil {
		for _, f := range zr.File {
			name := strings.ToLower(f.Name)
			for _, ext := range dangerousArchiveExtensions {
				if strings.HasSuffix(name, ext) {
					return ErrSuspiciousContent
				}
			}
		}
	}
	return nil
}

func randomFilename(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate upload filename: %w", err)
	}
	return hex.EncodeToString(b) + ext, nil
}
