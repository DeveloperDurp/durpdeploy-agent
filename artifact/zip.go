package artifact

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func ValidateZIP(ctx context.Context, filename string) error {
	return ValidateZIPWithPolicy(ctx, filename, resourcepolicy.Defaults().Artifact)
}

// ValidateZIPWithPolicy checks a ZIP against a validated resource snapshot.
func ValidateZIPWithPolicy(ctx context.Context, filename string, policy resourcepolicy.Artifact) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	return processZIP(ctx, filename, "", policy)
}

// ExtractZIP writes regular files only into a newly created private directory.
// Mount this directory's contents read-only/noexec before exposing them to steps.
func ExtractZIP(
	ctx context.Context,
	filename string,
) (directory string, err error) {
	return ExtractZIPWithPolicy(ctx, filename, resourcepolicy.Defaults().Artifact)
}

// ExtractZIPWithPolicy validates policy before creating a private extraction tree.
func ExtractZIPWithPolicy(ctx context.Context, filename string, policy resourcepolicy.Artifact) (directory string, err error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	directory, err = os.MkdirTemp(
		filepath.Dir(filename),
		"durpdeploy-artifact-",
	)
	if err != nil {
		return "", err
	}
	if err = processZIP(ctx, filename, directory, policy); err != nil {
		return "", errors.Join(err, os.RemoveAll(directory))
	}
	return directory, nil
}

func processZIP(ctx context.Context, filename, directory string, policy resourcepolicy.Artifact) (err error) {
	archive, file, err := openZIP(ctx, filename, policy)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	var root *os.Root
	if directory != "" {
		root, err = os.OpenRoot(directory)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, root.Close()) }()
	}
	if len(archive.File) == 0 || int64(len(archive.File)) > policy.Files {
		return ErrInvalid
	}
	kinds := make(map[string]zipNodeKind, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateZIPEntry(entry, kinds, policy.StagingNodes); err != nil {
			return err
		}
		if entry.UncompressedSize64 > uint64(policy.ExtractedBytes-total) {
			return ErrInvalid
		}
		count, err := processEntry(ctx, entry, root, policy.ExtractedBytes-total)
		if err != nil {
			return err
		}
		total += count
	}
	return nil
}

func processEntry(
	ctx context.Context,
	entry *zip.File,
	root *os.Root,
	remaining int64,
) (size int64, err error) {
	if entry.Mode().IsDir() {
		if root == nil {
			return 0, nil
		}
		return 0, root.MkdirAll(entry.Name, 0755)
	}
	reader, err := entry.Open()
	if err != nil {
		return 0, ErrInvalid
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	target := io.Discard
	if root != nil {
		if err := root.MkdirAll(path.Dir(entry.Name), 0755); err != nil {
			return 0, err
		}
		file, err := root.OpenFile(
			entry.Name,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0644,
		)
		if err != nil {
			return 0, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		target = file
	}
	size, err = io.Copy(
		target,
		io.LimitReader(contextReader{ctx, reader}, remaining+1),
	)
	if ctx.Err() != nil {
		return size, ctx.Err()
	}
	if err != nil || size > remaining {
		return size, ErrInvalid
	}
	return size, nil
}

type contextReader struct {
	ctx    context.Context // NOSONAR: short-lived io.Reader adapter; Read cannot accept a context.
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
