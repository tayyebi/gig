package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// ErrUnsupportedMediaType is returned when an upload's sniffed content type
// is not in the allowed image list.
var ErrUnsupportedMediaType = errors.New("unsupported file type")

// ErrFileTooLarge is returned when an upload exceeds the configured limit.
var ErrFileTooLarge = errors.New("file exceeds the maximum upload size")

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
	defer dst.Close()
	if _, err := io.Copy(dst, io.LimitReader(file, st.MaxBytes)); err != nil {
		os.Remove(absPath)
		return "", fmt.Errorf("write upload file: %w", err)
	}
	return relPath, nil
}

func randomFilename(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate upload filename: %w", err)
	}
	return hex.EncodeToString(b) + ext, nil
}
