package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
)

const MaxExtractedBinary = int64(128 << 20)

func ExtractExecutable(archiveData []byte, assetName, executableName string) ([]byte, error) {
	switch {
	case strings.HasSuffix(assetName, ".zip"):
		return extractZip(archiveData, executableName)
	case strings.HasSuffix(assetName, ".tar.gz"):
		return extractTarGZ(archiveData, executableName)
	default:
		return nil, typed(app.IssueDownloadInvalid, errors.New("unsupported release archive format"))
	}
}

func extractZip(data []byte, executableName string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("open zip: %w", err))
	}
	var result []byte
	for _, entry := range reader.File {
		if err := validateArchivePath(entry.Name); err != nil {
			return nil, typed(app.IssueDownloadInvalid, err)
		}
		mode := entry.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() || mode.Type() != 0 {
			return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("zip entry %q is not a regular file", entry.Name))
		}
		if path.Base(entry.Name) != executableName {
			continue
		}
		if result != nil {
			return nil, typed(app.IssueDownloadInvalid, errors.New("archive contains duplicate manager executables"))
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, typed(app.IssueDownloadInvalid, err)
		}
		result, err = readBounded(stream, MaxExtractedBinary)
		closeErr := stream.Close()
		if err != nil {
			return nil, typed(app.IssueDownloadInvalid, err)
		}
		if closeErr != nil {
			return nil, typed(app.IssueDownloadInvalid, closeErr)
		}
	}
	if result == nil {
		return nil, typed(app.IssueDownloadInvalid, errors.New("archive does not contain the manager executable"))
	}
	return result, nil
}

func extractTarGZ(data []byte, executableName string) ([]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("open gzip: %w", err))
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var result []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("read tar: %w", err))
		}
		if err := validateArchivePath(header.Name); err != nil {
			return nil, typed(app.IssueDownloadInvalid, err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("tar entry %q has unsafe type %d", header.Name, header.Typeflag))
		}
		if path.Base(header.Name) != executableName {
			continue
		}
		if result != nil {
			return nil, typed(app.IssueDownloadInvalid, errors.New("archive contains duplicate manager executables"))
		}
		if header.Size < 0 || header.Size > MaxExtractedBinary {
			return nil, typed(app.IssueDownloadInvalid, errors.New("manager executable exceeds extraction bound"))
		}
		result, err = readBounded(reader, MaxExtractedBinary)
		if err != nil {
			return nil, typed(app.IssueDownloadInvalid, err)
		}
		if int64(len(result)) != header.Size {
			return nil, typed(app.IssueDownloadInvalid, errors.New("manager executable is truncated"))
		}
	}
	if result == nil {
		return nil, typed(app.IssueDownloadInvalid, errors.New("archive does not contain the manager executable"))
	}
	return result, nil
}

func validateArchivePath(name string) error {
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || strings.HasPrefix(name, "/") || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, ":") {
		return fmt.Errorf("archive entry %q escapes the extraction root", name)
	}
	return nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("extracted file exceeds size bound")
	}
	return data, nil
}
