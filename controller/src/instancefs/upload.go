package instancefs

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type UploadTarget struct {
	dirRel     string
	fileName   string
	targetPath string
}

func (t UploadTarget) DirRel() string {
	return t.dirRel
}

func (t UploadTarget) FileName() string {
	return t.fileName
}

func (t UploadTarget) TargetPath() string {
	return t.targetPath
}

func (t UploadTarget) TargetDir() string {
	return filepath.Dir(t.targetPath)
}

func (fs *InstanceFS) ResolveUploadTarget(dirPath string, fileName string) (UploadTarget, error) {
	fileName, err := EnsureFileName(fileName)
	if err != nil {
		return UploadTarget{}, err
	}
	safeDir, err := fs.Resolve(dirPath)
	if err != nil {
		return UploadTarget{}, err
	}
	targetDir := safeDir.AbsPath()
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetDir, false); err != nil {
		return UploadTarget{}, err
	}
	if info, err := os.Lstat(targetDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return UploadTarget{}, ErrPathOutsideInstanceRoot
		}
		if !info.IsDir() {
			return UploadTarget{}, errors.New(msg.PathNotDirectory)
		}
		if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetDir, true); err != nil {
			return UploadTarget{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return UploadTarget{}, err
	}
	targetPath := filepath.Join(targetDir, fileName)
	return UploadTarget{dirRel: safeDir.RelSlash(), fileName: fileName, targetPath: targetPath}, nil
}

func (fs *InstanceFS) EnsureUploadTargetDirectory(target UploadTarget) error {
	targetDir := target.TargetDir()
	if strings.TrimSpace(targetDir) == "" {
		return errors.New(msg.EmptyDest)
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetDir, false); err != nil {
		return err
	}
	if err := EnsureDirectoryStepwise(fs.rootPath, targetDir, 0755); err != nil {
		return err
	}
	if err := fs.ensureResolvedPathWithinRoot(targetDir); err != nil {
		return &PathAccessError{Kind: PathAccessErrorWithinRoot, Err: err}
	}
	return nil
}

func (fs *InstanceFS) CheckUploadTargetFile(target UploadTarget, overwrite bool) error {
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, target.TargetPath(), false); err != nil {
		return err
	}
	info, err := os.Stat(target.TargetPath())
	if err == nil {
		if info.IsDir() {
			return ErrUploadTargetIsDirectory
		}
		if !overwrite {
			return os.ErrExist
		}
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (fs *InstanceFS) OpenUploadAtomicFile(target UploadTarget, mode os.FileMode) (*os.File, string, error) {
	return OpenAtomicTempFileWithinRoot(fs.rootPath, target.TargetPath(), mode)
}

func (fs *InstanceFS) OpenRegisteredUploadAtomicFile(target UploadTarget, mode os.FileMode) (*os.File, string, error) {
	targetPath := strings.TrimSpace(target.TargetPath())
	if targetPath == "" {
		return nil, "", errors.New(msg.EmptyDest)
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetPath, false); err != nil {
		return nil, "", err
	}
	dir := filepath.Dir(targetPath)
	if dir == "" {
		dir = "."
	}
	if err := EnsureDirectoryStepwise(fs.rootPath, dir, 0755); err != nil {
		return nil, "", err
	}
	tmp, tmpPath, err := file.CreateRegisteredTempFileForTarget(targetPath, mode)
	if err != nil {
		return nil, "", err
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, tmpPath, true); err != nil {
		_ = tmp.Close()
		_ = file.RemoveRegisteredTempPath(tmpPath)
		return nil, "", err
	}
	return tmp, tmpPath, nil
}

func (fs *InstanceFS) CommitUploadAtomicFile(tempPath string, target UploadTarget, overwrite bool) error {
	return CommitAtomicTempFileWithinRoot(fs.rootPath, tempPath, target.TargetPath(), overwrite)
}

func (fs *InstanceFS) CommitRegisteredUploadFile(tempPath string, target UploadTarget, overwrite bool) (bool, error) {
	tempPath = strings.TrimSpace(tempPath)
	targetPath := strings.TrimSpace(target.TargetPath())
	if tempPath == "" || targetPath == "" {
		return false, errors.New(msg.EmptyDest)
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, tempPath, true); err != nil {
		return false, err
	}
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetPath, false); err != nil {
		return false, err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, ErrPathOutsideInstanceRoot
		}
		if info.IsDir() {
			return false, ErrUploadTargetIsDirectory
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	stageInfo, err := os.Lstat(tempPath)
	if err != nil {
		return false, err
	}

	if overwrite {
		if err := compat.ReplaceFileAtomic(tempPath, targetPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
			if renameErr := compat.RenameNoReplace(tempPath, targetPath); renameErr != nil {
				return false, renameErr
			}
		}
	} else {
		if err := compat.RenameNoReplace(tempPath, targetPath); err != nil {
			return false, err
		}
	}

	committed := true
	if err := EnsurePathComponentsWithinRoot(fs.rootPath, targetPath, true); err != nil {
		_ = removeCommittedFileIfSame(targetPath, stageInfo)
		return false, err
	}
	if err := file.SyncDir(filepath.Dir(targetPath)); err != nil {
		return committed, err
	}
	return committed, nil
}
