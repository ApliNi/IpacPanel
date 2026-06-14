package instancefs

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type InstanceFS struct {
	rootPath string
}

func NewFromProcess(sp *process.InstanceProcess) (*InstanceFS, error) {
	rootPath, err := GetInstanceRootPath(sp)
	if err != nil {
		return nil, err
	}
	return &InstanceFS{rootPath: rootPath}, nil
}

func (fs *InstanceFS) RootPath() string {
	if fs == nil {
		return ""
	}
	return fs.rootPath
}

func (fs *InstanceFS) EvalRootReal() (string, error) {
	if fs == nil || strings.TrimSpace(fs.rootPath) == "" {
		return "", errors.New(msg.InstanceNotFound)
	}
	rootReal, err := filepath.EvalSymlinks(fs.rootPath)
	if err != nil {
		return "", fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	return rootReal, nil
}

func GetInstanceRootPath(sp *process.InstanceProcess) (string, error) {
	if sp == nil {
		return "", errors.New(msg.InstanceNotFound)
	}
	ins := sp.InstanceSnapshot()
	if strings.TrimSpace(ins.Name) == "" {
		return "", errors.New(msg.InstanceNotFound)
	}

	root := strings.TrimSpace(ins.Path)
	absRoot, err := cfg.ResolveInstancePath(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absRoot), nil
}

func ResolveInstanceFilePath(sp *process.InstanceProcess, relativePath string) (string, string, error) {
	fs, err := NewFromProcess(sp)
	if err != nil {
		return "", "", err
	}
	safePath, err := fs.Resolve(relativePath)
	if err != nil {
		return "", "", err
	}
	return fs.RootPath(), safePath.RelSlash(), nil
}

func ResolveFileListJumpPath(sp *process.InstanceProcess, requestedPath string) (string, string, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if textTooLong(requestedPath, maxFilePathTextLen) {
		return "", "", errors.New(msg.PathTooLong)
	}
	if requestedPath == "" {
		return ResolveInstanceFilePath(sp, "")
	}

	osPath := strings.ReplaceAll(requestedPath, "\\", string(filepath.Separator))
	osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
	windowsAbsolute := IsWindowsAbsolutePath(requestedPath)
	if windowsAbsolute && !filepath.IsAbs(osPath) {
		return "", "", ErrPathOutsideInstanceRoot
	}
	if !filepath.IsAbs(osPath) {
		return ResolveInstanceFilePath(sp, requestedPath)
	}

	rootPath, err := GetInstanceRootPath(sp)
	if err != nil {
		return "", "", err
	}
	targetPath := filepath.Clean(osPath)
	if err := EnsureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		return "", "", err
	}

	rootReal, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", "", fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	targetReal, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return "", "", fmt.Errorf(msg.PathInvalidFmt, err)
	}
	relToRoot, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return "", "", err
	}
	if relToRoot == "." {
		return rootPath, "", nil
	}
	return rootPath, filepath.ToSlash(relToRoot), nil
}
