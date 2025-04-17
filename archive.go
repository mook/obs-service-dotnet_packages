package main

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aibor/cpio"
	"github.com/klauspost/compress/zstd"
)

type compressionType string

const (
	compressionTypeNone = "none"
	compressionTypeGZip = "gz"
	compressionTypeZstd = "zst"
)

func (c *compressionType) String() string {
	if c == nil {
		return "<nil>"
	}
	return string(*c)
}

func (c *compressionType) Set(value string) error {
	switch value {
	case compressionTypeNone, compressionTypeGZip, compressionTypeZstd:
		*c = compressionType(value)
		return nil
	}
	return fmt.Errorf("invalid copmression type %s", value)
}

func createArchive(sourceDir, outputBase string, compressionType compressionType) error {
	var extension string
	compress := func(w io.Writer) (io.Writer, error) { return w, nil }
	switch compressionType {
	case compressionTypeNone:
		extension = ".tar"
	case compressionTypeGZip:
		extension = ".tar.gz"
		compress = func(w io.Writer) (io.Writer, error) { return gzip.NewWriter(w), nil }
	case compressionTypeZstd:
		extension = ".tar.zst"
		compress = func(w io.Writer) (io.Writer, error) { return zstd.NewWriter(w) }
	}

	outputPath := outputBase + extension
	temporaryPattern := filepath.Base(outputBase) + ".*" + extension
	outputFile, err := os.CreateTemp(filepath.Dir(outputBase), temporaryPattern)
	if err != nil {
		return err
	}
	defer func() {
		_ = outputFile.Close()
		_ = os.Remove(outputFile.Name())
	}()
	compressWriter, err := compress(outputFile)
	if err != nil {
		return err
	}
	tarWriter := tar.NewWriter(compressWriter)

	if err := tarWriter.AddFS(os.DirFS(sourceDir)); err != nil {
		return err
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if closer, ok := compressWriter.(io.Closer); ok {
		if err := closer.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			return err
		}
	}

	if err := outputFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}

	if err := os.Chmod(outputFile.Name(), 0o644); err != nil {
		return fmt.Errorf("failed to set permissions for package file: %w", err)
	}

	return os.Rename(outputFile.Name(), outputPath)
}

// Extract an archive, returning the names of the solution files.
func extractArchive(ctx context.Context, archivePath, outDir string) ([]string, error) {
	slog.InfoContext(ctx, "extracting archive", "archive", archivePath)
	switch filepath.Ext(archivePath) {
	case ".cpio", ".obscpio":
		return extractCpio(ctx, archivePath, outDir)
	case ".tar", ".tar.gz", ".tar.zst":
		return extractTar(ctx, archivePath, outDir)
	}
	return nil, fmt.Errorf("unsupported archive format %s", filepath.Ext(archivePath))
}

type fileInfo struct {
	name string // relative name including path; not (necessarily) base name.
	fs.FileInfo
	accessTime time.Time
	isLink     bool   // is a hard link (only for tar files)
	linkName   string // link target, for hard links and symlinks.
}

func writeFile(ctx context.Context, outDir string, reader io.Reader, fileInfo fileInfo) error {
	outPath := filepath.Join(outDir, fileInfo.name)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("failed to ensure parent directory %s: %w", filepath.Dir(outPath), err)
	}
	switch {
	case fileInfo.Mode().IsDir():
		if err := os.MkdirAll(outPath, fileInfo.Mode()); err != nil {
			return fmt.Errorf("error creating directory %s: %w", fileInfo.name, err)
		}
	case fileInfo.isLink:
		if err := os.Link(filepath.Join(outDir, fileInfo.linkName), outPath); err != nil {
			return fmt.Errorf("failed to create hard link %s: %w", fileInfo.name, err)
		}
	case fileInfo.Mode()&fs.ModeType == fs.ModeSymlink:
		if err := os.Symlink(filepath.Join(outDir, fileInfo.linkName), outPath); err != nil {
			return fmt.Errorf("failed to create symlink %s: %w", fileInfo.name, err)
		}
	case fileInfo.Mode()&fs.ModeType == 0:
		outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY, fileInfo.Mode()&fs.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create member %s: %w", fileInfo.name, err)
		}
		n, err := io.Copy(outFile, reader)
		if err != nil {
			return fmt.Errorf("failed to extract member %s: %w", fileInfo.name, err)
		}
		if n < fileInfo.Size() {
			return fmt.Errorf("short write extracting memeber %s: %d/%d bytes", fileInfo.name, n, fileInfo.Size())
		}
	default:
		slog.WarnContext(ctx, "skipping unsupported file type", "member", fileInfo.name)
		return nil
	}
	if err := os.Chmod(outPath, fileInfo.Mode()); err != nil {
		slog.WarnContext(ctx, "error setting file mode", "member", fileInfo.name, "error", err)
	}
	if err := os.Chtimes(outPath, fileInfo.accessTime, fileInfo.ModTime()); err != nil {
		slog.WarnContext(ctx, "failed to set file times", "member", fileInfo.name, "error", err)
	}
	return nil
}

func extractTar(ctx context.Context, archivePath, outDir string) ([]string, error) {
	rawReader, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer rawReader.Close()
	var decompressor io.Reader
	switch filepath.Ext(archivePath) {
	case ".tar":
		decompressor = rawReader
	case ".gz":
		decompressor, err = gzip.NewReader(rawReader)
	case ".bz2":
		decompressor = bzip2.NewReader(rawReader)
	case ".zst":
		decompressor, err = zstd.NewReader(rawReader)
	default:
		err = fmt.Errorf("could not detect tar compression for %s", archivePath)
	}
	if err != nil {
		return nil, err
	}

	var solutions []string
	reader := tar.NewReader(decompressor)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return solutions, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read archive %s: %w", archivePath, err)
		}
		fileInfo := fileInfo{
			name:       header.Name,
			FileInfo:   header.FileInfo(),
			accessTime: header.AccessTime,
			isLink:     header.Typeflag == tar.TypeLink,
			linkName:   header.Linkname,
		}
		if err := writeFile(ctx, outDir, reader, fileInfo); err != nil {
			return nil, err
		}
		if path.Ext(header.Name) == ".sln" {
			solutions = append(solutions, header.Name)
		}
	}
}

func extractCpio(ctx context.Context, archivePath, outDir string) ([]string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive %s: %w", archivePath, err)
	}
	defer file.Close()
	reader := cpio.NewReader(file)
	var solutions []string
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return solutions, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read cpio record: %w", err)
		}
		fileInfo := fileInfo{
			name:       header.Name,
			FileInfo:   header.FileInfo(),
			accessTime: time.Time{},
		}
		if fileInfo.Mode()&fs.ModeType == fs.ModeSymlink {
			buf, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("failed to read symlink %s: %w", header.Name, err)
			}
			fileInfo.linkName = string(buf)
		}
		if err := writeFile(ctx, outDir, reader, fileInfo); err != nil {
			return nil, err
		}
		if filepath.Ext(header.Name) == ".sln" {
			solutions = append(solutions, header.Name)
		}
	}
}
