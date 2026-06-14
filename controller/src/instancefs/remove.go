package instancefs

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"os"
	"path/filepath"
)

func (fs *InstanceFS) DeleteExisting(relativePath string) (SafePath, bool, error) {
	safePath, info, err := fs.ResolveRequiredExisting(relativePath)
	if err != nil {
		return SafePath{}, false, err
	}

	if info.IsDir() {
		return safePath, true, RemoveAllWithinRoot(fs.rootPath, safePath.AbsPath())
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

func RemoveAllWithinRoot(rootPath string, targetPath string) error {
	if err := EnsurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	if err := filepath.WalkDir(targetPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := EnsurePathComponentsWithinRoot(rootPath, path, true); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(targetPath)
}
