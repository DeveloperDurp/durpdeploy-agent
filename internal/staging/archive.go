// Package staging transports regular files in a deployment's private workspace.
package staging

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

const MaxBytes int64 = 512 << 20
const MaxArchive int64 = MaxBytes + (32 << 20)
const MaxFiles = 20000

var ErrInvalid = errors.New("invalid staging archive")

func Pack(
	ctx context.Context,
	directory string,
	target io.Writer,
) (result error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	writer := tar.NewWriter(target)
	defer func() { result = errors.Join(result, writer.Close()) }()
	var total int64
	var count int
	return fs.WalkDir(
		root.FS(),
		".",
		func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if name == "." {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !info.IsDir() || len(name) > 4096 ||
				strings.ContainsAny(name, "\\\x00") {
				return ErrInvalid
			}
			count++
			if count > MaxFiles {
				return ErrInvalid
			}
			header := &tar.Header{
				Name:     name,
				Mode:     0644,
				Size:     info.Size(),
				Typeflag: tar.TypeReg,
				Uid:      65534,
				Gid:      65534,
			}
			if info.IsDir() {
				header.Mode = 0755
				header.Size = 0
				header.Typeflag = tar.TypeDir
			}
			total += header.Size
			if total > MaxBytes {
				return ErrInvalid
			}
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(
				writer,
				&contextReader{ctx: ctx, source: file, remaining: -1},
				header.Size,
			)
			return errors.Join(copyErr, file.Close())
		},
	)
}

// Unpack validates even when directory is empty, before importing into a volume.
func Unpack(
	ctx context.Context,
	source io.Reader,
	directory string,
) (result error) {
	var root *os.Root
	if directory != "" {
		var err error
		root, err = os.OpenRoot(directory)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, root.Close()) }()
	}
	bounded := &contextReader{
		ctx:    ctx,
		source: io.LimitReader(source, MaxArchive+1),
	}
	reader := tar.NewReader(bounded)
	seen := make(map[string]bool)
	var total int64
	for {
		bounded.remaining = 16 << 20
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ErrInvalid
		}
		name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
		if (name == "." || name == "") && header.Typeflag == tar.TypeDir &&
			header.Size == 0 {
			continue
		}
		if !fs.ValidPath(name) || len(name) > 4096 ||
			strings.ContainsAny(name, "\\\x00") {
			return ErrInvalid
		}
		if _, exists := seen[name]; exists {
			return ErrInvalid
		}
		if len(seen) >= MaxFiles || header.Size < 0 ||
			header.Size > MaxBytes-total {
			return ErrInvalid
		}
		isDir := header.Typeflag == tar.TypeDir
		if !isDir && header.Typeflag != tar.TypeReg &&
			header.Typeflag != tar.TypeRegA ||
			isDir && header.Size != 0 {
			return ErrInvalid
		}
		seen[name] = isDir
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if dir, exists := seen[parent]; exists && !dir {
				return ErrInvalid
			}
		}
		total += header.Size
		bounded.remaining = header.Size
		if isDir {
			if root != nil {
				if err := root.MkdirAll(name, 0700); err != nil {
					return err
				}
			}
			continue
		}
		if root == nil {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return err
			}
			continue
		}
		if err := root.MkdirAll(path.Dir(name), 0700); err != nil {
			return err
		}
		file, err := root.OpenFile(
			name,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0600,
		)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
	bounded.remaining = 16 << 20
	trailer, err := io.ReadAll(io.LimitReader(bounded, (16<<20)+1))
	if err != nil || len(trailer) > 16<<20 {
		return ErrInvalid
	}
	for _, value := range trailer {
		if value != 0 {
			return ErrInvalid
		}
	}
	return nil
}

type contextReader struct {
	ctx       context.Context
	source    io.Reader
	remaining int64
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining == 0 {
		return 0, ErrInvalid
	}
	if r.remaining > 0 && int64(len(data)) > r.remaining {
		data = data[:r.remaining]
	}
	n, err := r.source.Read(data)
	if r.remaining > 0 {
		r.remaining -= int64(n)
	}
	return n, err
}
