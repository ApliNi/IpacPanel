package instancefs

import (
	"IpacPanel/controller/src/msg"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ExtractDirCache struct {
	fs       *InstanceFS
	rootPath string
	dirs     map[string]map[string]struct{}
}

func (fs *InstanceFS) ResolveExtractSource(relativePath string) (SafePath, os.FileInfo, error) {
	safePath, err := fs.ResolveRequired(relativePath)
	if err != nil {
		return SafePath{}, nil, err
	}
	info, err := os.Stat(safePath.AbsPath())
	if err != nil {
		return SafePath{}, nil, &PathAccessError{Kind: PathAccessErrorStat, Err: err}
	}
	if err := fs.ensureResolvedPathWithinRoot(safePath.AbsPath()); err != nil {
		return SafePath{}, nil, &PathAccessError{Kind: PathAccessErrorWithinRoot, Err: err}
	}
	if info.IsDir() {
		return SafePath{}, nil, &PathAccessError{Kind: PathAccessErrorDirectory, Err: errors.New(msg.TargetIsDirectory)}
	}
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveExtractTarget(relativePath string, extractHere bool, overwrite bool) (SafePath, error) {
	targetPath, err := fs.Resolve(relativePath)
	if err != nil {
		return SafePath{}, err
	}
	if strings.TrimSpace(targetPath.AbsPath()) == "" {
		return SafePath{}, errors.New(msg.EmptyDest)
	}
	if extractHere {
		info, err := os.Stat(targetPath.AbsPath())
		if err != nil {
			return SafePath{}, err
		}
		if !info.IsDir() {
			return SafePath{}, errors.New(msg.DestinationNotDirectory)
		}
		if err := fs.ensureResolvedPathWithinRoot(targetPath.AbsPath()); err != nil {
			return SafePath{}, err
		}
		if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetPath.AbsPath(), true); err != nil {
			return SafePath{}, ErrExtractTargetInvalidPath
		}
		return targetPath, nil
	}
	if overwrite {
		info, err := os.Stat(targetPath.AbsPath())
		if err == nil {
			if !info.IsDir() {
				return SafePath{}, errors.New(msg.DestinationNotDirectory)
			}
			if err := fs.ensureResolvedPathWithinRoot(targetPath.AbsPath()); err != nil {
				return SafePath{}, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return SafePath{}, err
		} else {
			if err := fs.ensureNewPathWithinRoot(targetPath.AbsPath()); err != nil {
				return SafePath{}, err
			}
		}
	} else {
		if err := fs.ensureNewPathWithinRoot(targetPath.AbsPath()); err != nil {
			return SafePath{}, err
		}
		if _, err := os.Stat(targetPath.AbsPath()); err == nil {
			return SafePath{}, os.ErrExist
		} else if !errors.Is(err, os.ErrNotExist) {
			return SafePath{}, err
		}
	}
	if _, err := os.Stat(targetPath.AbsPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return SafePath{}, err
	}
	return targetPath, nil
}

func (fs *InstanceFS) NewExtractDirCache() *ExtractDirCache {
	rootPath := ""
	if fs != nil {
		rootPath = fs.rootPath
	}
	return &ExtractDirCache{fs: fs, rootPath: rootPath, dirs: make(map[string]map[string]struct{})}
}

func (c *ExtractDirCache) Ensure(baseDir string, dir string) error {
	if c == nil || c.fs == nil {
		return errors.New(msg.EmptyDest)
	}
	if strings.TrimSpace(c.rootPath) == "" {
		return errors.New(msg.EmptyDest)
	}
	cleanBase := filepath.Clean(baseDir)
	clean := filepath.Clean(dir)
	if baseDirs, ok := c.dirs[cleanBase]; ok {
		if _, ok := baseDirs[clean]; ok {
			if err := EnsurePathComponentsWithinRoot(c.rootPath, clean, true); err != nil {
				return err
			}
			return EnsurePathComponentsWithinRoot(cleanBase, clean, true)
		}
	}
	if err := EnsureDirectoryWithinRoot(c.rootPath, clean); err != nil {
		return err
	}
	if err := EnsurePathComponentsWithinRoot(cleanBase, clean, true); err != nil {
		return err
	}
	if _, ok := c.dirs[cleanBase]; !ok {
		c.dirs[cleanBase] = make(map[string]struct{})
	}
	c.dirs[cleanBase][clean] = struct{}{}
	return nil
}

func (fs *InstanceFS) ResolveExtractOutputPath(baseDir string, entryName string) (string, error) {
	name := strings.TrimSpace(strings.ReplaceAll(entryName, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", nil
	}
	for _, part := range strings.Split(name, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", ErrArchiveInvalidPath
		}
	}
	dest := filepath.Join(baseDir, filepath.FromSlash(name))
	cleanBase := filepath.Clean(baseDir)
	cleanDest := filepath.Clean(dest)
	if cleanDest != cleanBase && !strings.HasPrefix(cleanDest, cleanBase+string(filepath.Separator)) {
		return "", ErrArchiveInvalidPath
	}
	return cleanDest, nil
}

func (fs *InstanceFS) EnsureExtractDirectory(baseDir string, dst string, dirCache *ExtractDirCache) error {
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, dst, false); err != nil {
		return ErrArchiveInvalidPath
	}
	if err := EnsurePathComponentsWithinRoot(baseDir, dst, true); err != nil {
		return ErrArchiveInvalidPath
	}
	if err := dirCache.Ensure(baseDir, dst); err != nil {
		return err
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, dst, true); err != nil {
		return ErrArchiveInvalidPath
	}
	if err := EnsurePathComponentsWithinRoot(baseDir, dst, true); err != nil {
		return ErrArchiveInvalidPath
	}
	return nil
}

func (fs *InstanceFS) WriteExtractedFile(ctx context.Context, baseDir string, dst string, mode os.FileMode, src io.Reader, dirCache *ExtractDirCache, overwrite bool, buffer []byte) (int64, error) {
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, dst, false); err != nil {
		return 0, ErrArchiveInvalidPath
	}
	if err := EnsurePathComponentsWithinRoot(baseDir, dst, false); err != nil {
		return 0, ErrArchiveInvalidPath
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	if err := dirCache.Ensure(baseDir, filepath.Dir(dst)); err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	temp, tempPath, err := OpenAtomicTempFileWithinRoot(fs.rootPath, dst, mode)
	if err != nil {
		if errors.Is(err, ErrPathOutsideInstanceRoot) {
			return 0, ErrArchiveInvalidPath
		}
		return 0, err
	}
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	var copyErr error
	var totalWritten int64
	for {
		select {
		case <-ctx.Done():
			copyErr = ctx.Err()
		default:
		}
		if copyErr != nil {
			break
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			select {
			case <-ctx.Done():
				copyErr = ctx.Err()
			default:
			}
			if copyErr != nil {
				break
			}
			written, writeErr := temp.Write(buffer[:n])
			if writeErr != nil {
				copyErr = writeErr
				break
			}
			if written != n {
				copyErr = io.ErrShortWrite
				break
			}
			totalWritten += int64(written)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			copyErr = readErr
			break
		}
	}
	if copyErr != nil {
		return totalWritten, copyErr
	}
	if err := temp.Sync(); err != nil {
		return totalWritten, err
	}
	if err := temp.Close(); err != nil {
		return totalWritten, err
	}
	if err := EnsurePathComponentsWithinRoot(baseDir, dst, false); err != nil {
		return totalWritten, ErrArchiveInvalidPath
	}
	if err := CommitAtomicTempFileWithinRoot(fs.rootPath, tempPath, dst, overwrite); err != nil {
		if errors.Is(err, ErrPathOutsideInstanceRoot) {
			return totalWritten, ErrArchiveInvalidPath
		}
		return totalWritten, err
	}
	return totalWritten, nil
}
