package instancefs

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeleteFailure records a single file or directory that failed to be deleted.
type DeleteFailure struct {
	Path   string // Instance-relative slash path of the entry that failed to delete.
	IsDir  bool
	Reason error // Underlying error.
}

// PartialDeleteError is returned when some entries could not be deleted but the
// operation completed as much as possible. It aggregates per-entry failures.
// Callers can use errors.As to inspect the failures.
type PartialDeleteError struct {
	Failures []DeleteFailure
}

func (e *PartialDeleteError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return msg.PartialDeleteFailed
	}
	return fmt.Sprintf(msg.PartialDeleteFailedCountFmt, len(e.Failures))
}

func (e *PartialDeleteError) Unwrap() error {
	if e == nil || len(e.Failures) == 0 {
		return nil
	}
	return e.Failures[0].Reason
}

func (e *PartialDeleteError) add(rootPath string, path string, isDir bool, reason error) {
	if reason == nil {
		return
	}
	e.Failures = append(e.Failures, DeleteFailure{Path: RelativeDeleteFailurePath(rootPath, path), IsDir: isDir, Reason: reason})
}

func RelativeDeleteFailurePath(rootPath string, path string) string {
	rootPath = filepath.Clean(rootPath)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(rootPath, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

func (fs *InstanceFS) DeleteExisting(relativePath string) (SafePath, bool, error) {
	safePath, info, err := fs.ResolveRequiredExisting(relativePath)
	if err != nil {
		return SafePath{}, false, err
	}

	if info.IsDir() {
		return safePath, true, RemoveAllWithinRootBestEffort(fs.rootPath, safePath.AbsPath())
	}
	return safePath, false, RemoveFileWithinRoot(fs.rootPath, safePath.AbsPath())
}

func RemoveFileWithinRoot(rootPath string, targetPath string) error {
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ErrUploadTargetIsDirectory
	}
	return os.Remove(targetPath)
}

func RemoveEmptyDirectoryWithinRoot(rootPath string, targetPath string) error {
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New(msg.DestinationNotDirectory)
	}
	return os.Remove(targetPath)
}

// RemoveAllWithinRootBestEffort walks the directory tree rooted at targetPath
// and deletes every file and directory entry. Symlinks are skipped (safety
// check). Instead of aborting on the first error, it continues and aggregates
// all failures into a *PartialDeleteError (nil if all succeeded).
//
// The function does NOT roll back: entries that were successfully deleted stay
// deleted. The returned error (if any) describes only the entries that could
// not be removed.
func RemoveAllWithinRootBestEffort(rootPath string, targetPath string) error {
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}

	type entryInfo struct {
		path    string
		isDir   bool
		walkErr error
	}
	entries := make([]entryInfo, 0)

	walkErr := filepath.WalkDir(targetPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// Record the walk error but do not abort the walk; we still want to
			// attempt deletion of other entries.
			isDir := entry != nil && entry.IsDir()
			entries = append(entries, entryInfo{path: path, isDir: isDir, walkErr: err})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if pathErr := EnsurePathComponentsWithinRoot(rootPath, path, true); pathErr != nil {
			entries = append(entries, entryInfo{path: path, walkErr: pathErr})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			entries = append(entries, entryInfo{path: path, walkErr: ErrPathOutsideInstanceRoot})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries = append(entries, entryInfo{path: path, isDir: entry.IsDir()})
		return nil
	})
	if walkErr != nil {
		// If the initial walk itself fails completely, return early.
		return walkErr
	}

	// Phase 2: delete all entries, best-effort.
	// Walk the list in reverse so directories are processed bottom-up.
	partial := &PartialDeleteError{}

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.walkErr != nil {
			partial.add(rootPath, e.path, e.isDir, e.walkErr)
			continue
		}
		if e.isDir {
			rmErr := RemoveEmptyDirectoryWithinRoot(rootPath, e.path)
			if rmErr != nil {
				if errors.Is(rmErr, os.ErrNotExist) {
					continue
				}
				partial.add(rootPath, e.path, true, rmErr)
			}
			continue
		}
		rmErr := RemoveFileWithinRoot(rootPath, e.path)
		if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			partial.add(rootPath, e.path, false, rmErr)
		}
	}

	if len(partial.Failures) > 0 {
		return partial
	}
	return nil
}
