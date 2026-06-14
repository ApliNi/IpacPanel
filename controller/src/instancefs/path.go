package instancefs

import (
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxFileNameLen     = 255
	maxFilePathTextLen = 4096
)

type SafePath struct {
	absPath  string
	relSlash string
}

func (p SafePath) AbsPath() string {
	return p.absPath
}

func (p SafePath) RelSlash() string {
	return p.relSlash
}

func (fs *InstanceFS) Resolve(relativePath string) (SafePath, error) {
	if fs == nil || strings.TrimSpace(fs.rootPath) == "" {
		return SafePath{}, errors.New(msg.InstanceNotFound)
	}

	relativePath = strings.TrimSpace(relativePath)
	if textTooLong(relativePath, maxFilePathTextLen) {
		return SafePath{}, errors.New(msg.PathTooLong)
	}
	if relativePath != "" {
		osPath := strings.ReplaceAll(relativePath, "\\", string(filepath.Separator))
		osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
		if filepath.IsAbs(osPath) || IsWindowsAbsolutePath(relativePath) || strings.HasPrefix(relativePath, "/") || strings.HasPrefix(relativePath, "\\") {
			return SafePath{}, ErrPathOutsideInstanceRoot
		}
	}

	normalizedRelative := NormalizeRelativeFilePath(relativePath)
	targetPath := fs.rootPath
	if normalizedRelative != "" {
		targetPath = filepath.Join(fs.rootPath, normalizedRelative)
	}
	targetPath = filepath.Clean(targetPath)

	relToRoot, err := filepath.Rel(fs.rootPath, targetPath)
	if err != nil {
		return SafePath{}, err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return SafePath{}, ErrPathOutsideInstanceRoot
	}

	relSlash := ""
	if relToRoot != "." {
		relSlash = filepath.ToSlash(relToRoot)
	}
	return SafePath{absPath: targetPath, relSlash: relSlash}, nil
}

func (fs *InstanceFS) ResolveExisting(relativePath string) (SafePath, os.FileInfo, error) {
	safePath, err := fs.Resolve(relativePath)
	if err != nil {
		return SafePath{}, nil, err
	}
	info, err := os.Stat(safePath.AbsPath())
	if err != nil {
		return SafePath{}, nil, err
	}
	if err := fs.ensureResolvedPathWithinRoot(safePath.AbsPath()); err != nil {
		return SafePath{}, nil, err
	}
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveRequired(relativePath string) (SafePath, error) {
	safePath, err := fs.Resolve(relativePath)
	if err != nil {
		return SafePath{}, &PathAccessError{Kind: PathAccessErrorResolve, Err: err}
	}
	if safePath.RelSlash() == "" {
		return SafePath{}, &PathAccessError{Kind: PathAccessErrorRequired, Err: errors.New(msg.FilePathRequired)}
	}
	return safePath, nil
}

func (fs *InstanceFS) ResolveRequiredWithinRoot(relativePath string) (SafePath, error) {
	safePath, err := fs.ResolveRequired(relativePath)
	if err != nil {
		return SafePath{}, err
	}
	if err := fs.ensureResolvedPathWithinRoot(safePath.AbsPath()); err != nil {
		return SafePath{}, &PathAccessError{Kind: PathAccessErrorWithinRoot, Err: err}
	}
	return safePath, nil
}

func (fs *InstanceFS) ResolveRequiredExisting(relativePath string) (SafePath, os.FileInfo, error) {
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
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveRequiredExistingFile(relativePath string) (SafePath, os.FileInfo, error) {
	safePath, info, err := fs.ResolveRequiredExisting(relativePath)
	if err != nil {
		return SafePath{}, nil, err
	}
	if info.IsDir() {
		return SafePath{}, nil, &PathAccessError{Kind: PathAccessErrorDirectory, Err: errors.New(msg.TargetIsDirectory)}
	}
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveExistingFile(relativePath string) (SafePath, os.FileInfo, error) {
	safePath, info, err := fs.ResolveExisting(relativePath)
	if err != nil {
		return SafePath{}, nil, err
	}
	if info.IsDir() {
		return SafePath{}, nil, errors.New(msg.TargetIsDirectory)
	}
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveExistingDir(relativePath string) (SafePath, os.FileInfo, error) {
	safePath, info, err := fs.ResolveExisting(relativePath)
	if err != nil {
		return SafePath{}, nil, err
	}
	if !info.IsDir() {
		return SafePath{}, nil, errors.New(msg.PathNotDirectory)
	}
	return safePath, info, nil
}

func (fs *InstanceFS) ResolveNewChild(parentRelativePath string, name string) (SafePath, SafePath, error) {
	parentPath, err := fs.Resolve(parentRelativePath)
	if err != nil {
		return SafePath{}, SafePath{}, err
	}
	parentInfo, err := os.Stat(parentPath.AbsPath())
	if err != nil {
		return SafePath{}, SafePath{}, err
	}
	if !parentInfo.IsDir() {
		return SafePath{}, SafePath{}, errors.New(msg.PathNotDirectory)
	}
	if err := fs.ensureResolvedPathWithinRoot(parentPath.AbsPath()); err != nil {
		return SafePath{}, SafePath{}, err
	}
	childAbsPath := filepath.Join(parentPath.AbsPath(), name)
	if err := fs.ensureNewPathWithinRoot(childAbsPath); err != nil {
		return SafePath{}, SafePath{}, err
	}
	childRelPath := name
	if parentPath.RelSlash() != "" {
		childRelPath = parentPath.RelSlash() + "/" + name
	}
	return parentPath, SafePath{absPath: childAbsPath, relSlash: childRelPath}, nil
}

func (fs *InstanceFS) ensureResolvedPathWithinRoot(targetPath string) error {
	if fs == nil || strings.TrimSpace(fs.rootPath) == "" {
		return errors.New(msg.InstanceNotFound)
	}
	rootReal, err := filepath.EvalSymlinks(fs.rootPath)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	targetReal, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !IsPathWithinRoot(rootReal, targetReal) {
		return ErrPathOutsideInstanceRoot
	}
	return nil

}

func (fs *InstanceFS) ensureNewPathWithinRoot(targetPath string) error {
	if fs == nil || strings.TrimSpace(fs.rootPath) == "" {
		return errors.New(msg.InstanceNotFound)
	}
	rootReal, err := filepath.EvalSymlinks(fs.rootPath)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	parent := filepath.Dir(targetPath)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !IsPathWithinRoot(rootReal, parentReal) {
		return ErrPathOutsideInstanceRoot
	}
	return nil
}

func IsPathWithinRoot(rootPath string, targetPath string) bool {
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func SameCleanPath(a string, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func EnsureResolvedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	rootAbs, err := GetInstanceRootPath(sp)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	targetReal, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !IsPathWithinRoot(rootReal, targetReal) {
		return ErrPathOutsideInstanceRoot
	}
	return nil
}

func EnsureNewPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	rootAbs, err := GetInstanceRootPath(sp)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}

	parent := filepath.Dir(targetPath)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !IsPathWithinRoot(rootReal, parentReal) {
		return ErrPathOutsideInstanceRoot
	}
	return nil
}

func EnsurePathComponentsWithinRoot(rootPath string, targetPath string, includeLeaf bool) error {
	rootReal, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	cleanRoot := filepath.Clean(rootReal)
	cleanTarget := filepath.Clean(targetPath)
	if cleanTarget == cleanRoot {
		return nil
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ErrPathOutsideInstanceRoot
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return nil
	}
	limit := len(parts)
	if !includeLeaf {
		limit--
	}
	current := cleanRoot
	for i := 0; i < limit; i++ {
		part := parts[i]
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf(msg.PathInvalidFmt, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
		realCurrent, realErr := filepath.EvalSymlinks(current)
		if realErr != nil {
			return fmt.Errorf(msg.PathInvalidFmt, realErr)
		}
		if !IsPathWithinRoot(cleanRoot, realCurrent) {
			return ErrPathOutsideInstanceRoot
		}
	}
	return nil
}

// EnsureDirectoryStepwise creates directory components one level at a time,
// verifying each level stays within root and does not traverse symlinks.
//
// This is a TOCTOU mitigation (not a full fix) against os.MkdirAll following
// symlinks that may have been concurrently swapped. A complete solution would
// require fd‑based openat/no‑follow primitives.
func EnsureDirectoryStepwise(rootPath string, dirPath string, mode os.FileMode) error {
	rootReal, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	cleanRoot := filepath.Clean(rootReal)
	cleanDir := filepath.Clean(dirPath)
	if cleanDir == cleanRoot {
		return nil
	}
	if !IsPathWithinRoot(cleanRoot, cleanDir) {
		return ErrPathOutsideInstanceRoot
	}

	rel, err := filepath.Rel(cleanRoot, cleanDir)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	parts := strings.Split(rel, string(filepath.Separator))

	current := cleanRoot
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)

		info, lerr := os.Lstat(current)
		if lerr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return ErrPathOutsideInstanceRoot
			}
			if !info.IsDir() {
				return fmt.Errorf("path component is not a directory: %s", current)
			}
			realCurrent, rerr := filepath.EvalSymlinks(current)
			if rerr != nil {
				return fmt.Errorf(msg.PathInvalidFmt, rerr)
			}
			if !IsPathWithinRoot(cleanRoot, realCurrent) {
				return ErrPathOutsideInstanceRoot
			}
			continue
		}
		if !errors.Is(lerr, os.ErrNotExist) {
			return fmt.Errorf(msg.PathInvalidFmt, lerr)
		}
		if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, lerr = os.Lstat(current)
		if lerr != nil {
			return fmt.Errorf(msg.PathInvalidFmt, lerr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrPathOutsideInstanceRoot
		}
		if !info.IsDir() {
			return fmt.Errorf("created path component is not a directory: %s", current)
		}
		realCurrent, rerr := filepath.EvalSymlinks(current)
		if rerr != nil {
			return fmt.Errorf(msg.PathInvalidFmt, rerr)
		}
		if !IsPathWithinRoot(cleanRoot, realCurrent) {
			return ErrPathOutsideInstanceRoot
		}
	}
	return nil
}

func NormalizeRelativeFilePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.TrimPrefix(p, "/")
	p = filepath.Clean(strings.ReplaceAll(p, "/", string(filepath.Separator)))
	if p == "." {
		return ""
	}
	return p
}

func IsWindowsAbsolutePath(p string) bool {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) < 3 {
		return false
	}
	drive := p[0]
	if !((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) {
		return false
	}
	return p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func EnsureFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New(msg.FileNameRequired)
	}
	if textTooLong(name, maxFileNameLen) {
		return "", errors.New(msg.FileNameTooLong)
	}
	if name == "." || name == ".." {
		return "", errors.New(msg.FileNameInvalid)
	}
	if strings.ContainsAny(name, `\\/:*?"<>|`) {
		return "", errors.New(msg.FileNameInvalidChars)
	}
	return name, nil
}

func textTooLong(value string, maxLen int) bool {
	return utf8.RuneCountInString(value) > maxLen
}
