package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

var options struct {
	verbose     bool
	tag         string
	archive     string
	compression compressionType
	output      string
	outDir      string
}

func initializeOptions() error {
	options.compression = compressionTypeGZip
	flag.BoolVar(&options.verbose, "verbose", false, "Enable extra logging")
	flag.StringVar(&options.tag, "tag", "9.0", "dotnet version to run")
	flag.StringVar(&options.archive, "archive", "", "Source code archive to scan for references")
	flag.Var(&options.compression, "compression", "Compression to use")
	flag.StringVar(&options.output, "output", "packages", "Base name of output archive")
	flag.StringVar(&options.outDir, "outdir", "", "Output directory")
	flag.Parse()
	return nil
}

// If the archive option was not provided, try to find an appropriate archive to
// use. Modifies [options.archive].
func locateArchive(ctx context.Context) error {
	if options.archive != "" {
		return nil
	}
	specFiles, err := filepath.Glob("*.spec")
	if err != nil {
		return fmt.Errorf("failed to detect spec files: %w", err)
	}

	exts := []string{
		".obscpio",
		".tar",
		".tar.gz",
		".tar.zst",
	}

	for _, specFile := range specFiles {
		stem := strings.TrimSuffix(specFile, ".spec")
		if strings.HasPrefix(stem, "_service:") {
			stem = stem[strings.LastIndex(stem, ":"):]
		}
		for _, pattern := range []string{stem, "_service:*" + stem} {
			for _, ext := range exts {
				slog.InfoContext(ctx, "globbing", "pattern", pattern+"*"+ext)
				names, err := filepath.Glob(pattern + "*" + ext)
				if err != nil {
					slog.ErrorContext(ctx, "glob failed", "error", err)
				} else {
					for _, archive := range names {
						slog.InfoContext(ctx, "got archive", "name", archive)
						options.archive = archive
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("failed to auto-detect archive name")
}
