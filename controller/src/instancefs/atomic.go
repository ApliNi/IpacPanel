package instancefs

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func OpenAtomicTempFileWithinRoot(rootPath string, targetPath string, mode os.FileMode) (*os.File, string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, "", errors.New(msg.TargetPathEmpty)
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return nil, "", err
	}
	dir := filepath.Dir(targetPath)
	if dir == "" {
		dir = "."
	}
	if err := EnsureDirectoryStepwise(rootPath, dir, 0755); err != nil {
		return nil, "", err
	}
	tmp, tmpPath, err := file.OpenTempForTarget(targetPath, mode)
	if err != nil {
		return nil, "", err
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, tmpPath, true); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return nil, "", err
	}
	return tmp, tmpPath, nil
}

func CommitAtomicTempFileWithinRoot(rootPath string, tempPath string, targetPath string, overwrite bool) error {
	tempPath = strings.TrimSpace(tempPath)
	targetPath = strings.TrimSpace(targetPath)
	if tempPath == "" || targetPath == "" {
		return errors.New(msg.TempPathAndTargetPathRequired)
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, tempPath, true); err != nil {
		return err
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
		if info.IsDir() {
			return ErrUploadTargetIsDirectory
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tempInfo, err := os.Lstat(tempPath)
	if err != nil {
		return err
	}
	if err := file.CommitTemp(tempPath, targetPath, overwrite, false); err != nil {
		return err
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		_ = removeCommittedFileIfSame(targetPath, tempInfo)
		return err
	}
	return file.SyncDir(filepath.Dir(targetPath))
}

func removeCommittedFileIfSame(targetPath string, committedInfo os.FileInfo) error {
	if committedInfo == nil {
		return nil
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 || targetInfo.IsDir() {
		return nil
	}
	if !os.SameFile(committedInfo, targetInfo) {
		return nil
	}
	return os.Remove(targetPath)
}

func CommitAtomicTempDirWithinRoot(rootPath string, tempDir string, targetPath string, overwrite bool) error {
	tempDir = strings.TrimSpace(tempDir)
	targetPath = strings.TrimSpace(targetPath)
	if tempDir == "" || targetPath == "" {
		return errors.New(msg.TempDirectoryAndTargetPathRequired)
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := file.CommitTempDir(tempDir, targetPath, file.DirOptions{Overwrite: overwrite, SyncDir: true}); err != nil {
		return err
	}
	return EnsurePathComponentsWithinRoot(rootPath, targetPath, true)
}

func EnsureDirectoryWithinRoot(rootPath string, dirPath string) error {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return errors.New(msg.DirectoryPathEmpty)
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, dirPath, false); err != nil {
		return err
	}
	if err := EnsureDirectoryStepwise(rootPath, dirPath, 0755); err != nil {
		return err
	}
	return EnsurePathComponentsWithinRoot(rootPath, dirPath, true)
}

// openSourceFileSafe opens srcPath for reading after verifying it is a regular
// file (not a symlink), the path components are within root, and the opened
// file matches the Lstat inode.
func openSourceFileSafe(rootPath string, srcPath string) (*os.File, error) {
	lsInfo, err := os.Lstat(srcPath)
	if err != nil {
		return nil, err
	}
	if lsInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrPathOutsideInstanceRoot
	}
	if !lsInfo.Mode().IsRegular() {
		return nil, ErrPathOutsideInstanceRoot
	}
	if err := EnsurePathComponentsWithinRoot(rootPath, srcPath, true); err != nil {
		return nil, err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(lsInfo, fi) {
		f.Close()
		return nil, ErrPathOutsideInstanceRoot
	}
	return f, nil
}

func OpenExistingFileSafe(rootPath string, srcPath string) (*os.File, os.FileInfo, error) {
	f, err := openSourceFileSafe(rootPath, srcPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func CopyFileAtomicWithinRoot(rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	tmp, tmpPath, err := OpenAtomicTempFileWithinRoot(rootPath, dstPath, mode)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := openSourceFileSafe(rootPath, srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := CommitAtomicTempFileWithinRoot(rootPath, tmpPath, dstPath, overwrite); err != nil {
		return err
	}
	committed = true
	return nil
}

func CopyFileAtomicWithinRootContext(ctx context.Context, rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp, tmpPath, err := OpenAtomicTempFileWithinRoot(rootPath, dstPath, mode)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := openSourceFileSafe(rootPath, srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			written := 0
			for written < n {
				if err := ctx.Err(); err != nil {
					return err
				}
				wn, writeErr := tmp.Write(buf[written:n])
				if writeErr != nil {
					return writeErr
				}
				if wn == 0 {
					return io.ErrShortWrite
				}
				written += wn
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := CommitAtomicTempFileWithinRoot(rootPath, tmpPath, dstPath, overwrite); err != nil {
		return err
	}
	committed = true
	return nil
}

func RenameFileOnlyWithinRoot(rootPath string, srcPath string, dstPath string, overwrite bool) error {
	if err := EnsurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
		return err
	}
	info, err := os.Lstat(dstPath)
	if err == nil {
		if !overwrite {
			return os.ErrExist
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
		if info.IsDir() {
			return ErrUploadTargetIsDirectory
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if overwrite {
		if err := compat.ReplaceFileAtomic(srcPath, dstPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if renameErr := compat.RenameNoReplace(srcPath, dstPath); renameErr != nil {
				return renameErr
			}
		}
	} else {
		if err := compat.RenameNoReplace(srcPath, dstPath); err != nil {
			return err
		}
	}
	if err := file.SyncDir(filepath.Dir(dstPath)); err != nil {
		return err
	}
	return file.SyncDir(filepath.Dir(srcPath))
}

func WriteFileAtomicWithinRoot(rootPath string, targetPath string, data []byte, overwrite bool, mode os.FileMode) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return errors.New(msg.TargetPathEmpty)
	}
	tmp, tmpPath, err := OpenAtomicTempFileWithinRoot(rootPath, targetPath, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if len(data) > 0 {
		if _, err := tmp.Write(data); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return CommitAtomicTempFileWithinRoot(rootPath, tmpPath, targetPath, overwrite)
}
