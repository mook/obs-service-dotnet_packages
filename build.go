package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

func build(ctx context.Context) error {
	srcDir, err := os.MkdirTemp("", "obs-service-dotnet-packages-src-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(srcDir)
	solutions, err := extractArchive(ctx, options.archive, srcDir)
	if err != nil {
		return err
	}
	outDir, err := os.MkdirTemp("", "obs-service-dotnet-packages-out-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outDir)

	dc, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	c, err := dc.ContainerCreate(
		ctx,
		&container.Config{
			Cmd:        []string{"sleep", "inf"},
			Image:      "registry.suse.com/bci/dotnet-sdk:" + options.tag,
			WorkingDir: "/src",
		},
		&container.HostConfig{
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: srcDir,
					Target: "/src",
					BindOptions: &mount.BindOptions{
						CreateMountpoint: true,
					},
				},
				{
					Type:   mount.TypeBind,
					Source: outDir,
					Target: "/out",
					BindOptions: &mount.BindOptions{
						CreateMountpoint: true,
					},
				},
			},
			AutoRemove: true,
		},
		nil,
		nil,
		"")
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}
	defer func() {
		err := dc.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
		if err != nil {
			slog.ErrorContext(ctx, "failed to remove container", "error", err)
		}
	}()

	if err := dc.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
		return err
	}

	// Use a local function here to ensure we always set permissions after
	// running dotnet restore.
	err = func() error {
		defer func() {
			if err := setPermissions(ctx, dc, c.ID); err != nil {
				slog.ErrorContext(
					ctx,
					"failed to reset permissions, temporary files may be left behind",
					"error", err,
					"src", srcDir,
					"out", outDir,
				)
			}
		}()

		for _, solution := range solutions {
			if err := restore(ctx, dc, c.ID, solution); err != nil {
				return fmt.Errorf("error restoring %s: %w", solution, err)
			}
		}

		return nil
	}()
	if err != nil {
		return err
	}

	if err := cleanup(ctx, outDir); err != nil {
		slog.WarnContext(ctx, "failed to clean up, archive might be larger than needed", "error", err)
	}

	outBase := options.output
	if options.outDir != "" {
		outBase = filepath.Join(options.outDir, options.output)
	}
	slog.InfoContext(ctx, "creating output archive", "base name", outBase)
	if err := createArchive(outDir, outBase, options.compression); err != nil {
		return fmt.Errorf("error creating output archive: %w", err)
	}
	return nil
}

func execInContainer(ctx context.Context, dc *client.Client, containerID string, cmd ...string) error {
	exec, err := dc.ContainerExecCreate(
		ctx,
		containerID,
		container.ExecOptions{
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
			Cmd:          cmd,
		})
	if err != nil {
		return err
	}
	resp, err := dc.ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{Tty: true})
	if err != nil {
		return err
	}
	if err := dc.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{Tty: true}); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Reader)
	return nil
}

func restore(ctx context.Context, dc *client.Client, containerID, solutionPath string) error {
	slog.InfoContext(ctx, "restoring solution", "solution", solutionPath)
	return execInContainer(
		ctx, dc, containerID,
		"dotnet", "restore", solutionPath,
		"--packages", "/out",
		"--verbosity", "detailed",
		"--locked-mode")
}

func setPermissions(ctx context.Context, dc *client.Client, containerID string) error {
	slog.InfoContext(ctx, "resetting file permissions")
	return execInContainer(
		ctx, dc, containerID,
		"chown", "--recursive", "--reference=/src", "/src", "/out")
}

func cleanup(ctx context.Context, workDir string) error {
	slog.InfoContext(ctx, "removing extraneous files")
	match := func(path string, patterns ...string) bool {
		for _, pattern := range patterns {
			if m, err := filepath.Match(pattern, path); err != nil {
				panic(fmt.Sprintf("bad pattern %q", pattern))
			} else if m {
				return true
			}
		}
		return false
	}
	return fs.WalkDir(os.DirFS(workDir), ".", func(path string, d fs.DirEntry, err error) error {
		switch {
		case match(path, "*", "*/*"):
			if d.IsDir() {
				return nil
			}
			slog.DebugContext(ctx, "removing non-directory", "path", path)
			if err := os.RemoveAll(filepath.Join(workDir, path)); err != nil {
				return fmt.Errorf("failed to remove file %s: %w", path, err)
			}
			return nil
		case match(path, "*/*/*.nupkg", "*/*/*.nupkg.sha512", "*/*/*.nuspec"):
			if d.IsDir() {
				slog.DebugContext(ctx, "removing directory", "path", path)
				if err := os.RemoveAll(filepath.Join(workDir, path)); err != nil {
					return fmt.Errorf("failed to remove directory %s: %w", path, err)
				}
				return fs.SkipDir
			}
			return nil
		default:
			slog.DebugContext(ctx, "removing extra", "path", path)
			if err := os.RemoveAll(filepath.Join(workDir, path)); err != nil {
				return fmt.Errorf("failed to remove %s: %w", path, err)
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
	})
}
