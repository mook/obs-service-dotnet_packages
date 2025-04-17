// Command dotnet-packages parses a tarball of donet source code and downloads
// referenced packages from NuGet, creating a tarball to be used with
// `dotnet restore --source`.
package main

import (
	"context"
	"log/slog"
	"os"
)

func run(ctx context.Context) error {
	if err := initializeOptions(); err != nil {
		return err
	}

	logOptions := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if options.verbose {
		logOptions.Level = slog.LevelDebug
	}
	// logger := slog.New(logging.NewJSONHandler(os.Stdout, logOptions))
	logger := slog.New(slog.NewTextHandler(os.Stderr, logOptions))
	slog.SetDefault(logger)

	if err := locateArchive(ctx); err != nil {
		return err
	}

	err := build(ctx)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("package download failed", "error", err)
		os.Exit(1)
	}
}
