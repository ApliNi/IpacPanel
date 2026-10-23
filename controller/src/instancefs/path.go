package instancefs

import (
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if strings.ContainsRune(relativePath, '\x00') {
		return SafePath{}, ErrPathOutsideInstanceRoot
	}
	if runtime.GOOS == "windows" && strings.Contains(relativePath, ":") {
		return SafePath{}, ErrPathOutsideInstanceRoot
	}
	if relativePath != "" {
		osPath := strings.ReplaceAll(relativePath, "\\", string(filepath.Separator))
		osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
		if filepath.IsAbs(osPath) || IsWindowsAbsolutePath(relativePath) || strings.HasPrefix(relativePath, "/") || strings.HasPrefix(relativePath, "\\") {
			return SafePath{}, ErrPathOutsideInstanceRoot
		}
	}

	normalizedRelative := NormalizeRelativeFilePath(relativePath)
	if HasWindowsReservedDeviceComponent(normalizedRelative) {
		return SafePath{}, ErrPathOutsideInstanceRoot
	}
	cleanRoot := cleanAbsPath(fs.rootPath)
	targetPath := cleanRoot
	if normalizedRelative != "" {
		targetPath = filepath.Join(cleanRoot, normalizedRelative)
	}
	targetPath = filepath.Clean(targetPath)

	relToRoot, err := filepath.Rel(cleanRoot, targetPath)
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
	name, err := EnsureFileName(name)
	if err != nil {
		return SafePath{}, SafePath{}, err
	}
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
	rootReal, err := filepath.EvalSymlinks(cleanAbsPath(fs.rootPath))
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	targetReal, err := filepath.EvalSymlinks(cleanAbsPath(targetPath))
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
	rootReal, err := filepath.EvalSymlinks(cleanAbsPath(fs.rootPath))
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	parent := filepath.Dir(cleanAbsPath(targetPath))
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !IsPathWithinRoot(rootReal, parentReal) {
		return ErrPathOutsideInstanceRoot
	}
	return nil
}

func stripExtendedLengthPrefix(p string) string {
	p = strings.TrimSpace(p)
	if runtime.GOOS != "windows" {
		return p
	}
	if len(p) >= 8 && strings.EqualFold(strings.ReplaceAll(p[:8], "/", `\`), `\\?\UNC\`) {
		return `\\` + p[8:]
	}
	if len(p) >= 4 && strings.ReplaceAll(p[:4], "/", `\`) == `\\?\` {
		return p[4:]
	}
	return p
}

func cleanAbsPath(p string) string {
	p = stripExtendedLengthPrefix(p)
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		if abs, err := filepath.Abs(clean); err == nil {
			clean = filepath.Clean(stripExtendedLengthPrefix(abs))
		}
	}
	return clean
}

func relativePathWithinBase(basePath string, targetPath string) (string, bool) {
	if basePath == "" || targetPath == "" {
		return "", false
	}
	rel, err := filepath.Rel(basePath, targetPath)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func IsPathWithinRoot(rootPath string, targetPath string) bool {
	_, ok := relativePathWithinBase(cleanAbsPath(rootPath), cleanAbsPath(targetPath))
	return ok
}

func SameCleanPath(a string, b string) bool {
	cleanA := cleanAbsPath(a)
	cleanB := cleanAbsPath(b)
	if cleanA == cleanB {
		return true
	}
	if cleanA == "" || cleanB == "" {
		return false
	}
	rel, err := filepath.Rel(cleanA, cleanB)
	return err == nil && rel == "."
}

func hasNonLeadingDotDot(p string) bool {
	p = stripExtendedLengthPrefix(p)
	if p == "" {
		return false
	}
	isAbs := filepath.IsAbs(p) || IsWindowsAbsolutePath(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
	normalized := strings.ReplaceAll(p, `\`, "/")
	seenNormal := isAbs
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if seenNormal {
				return true
			}
			continue
		}
		seenNormal = true
	}
	return false
}

func alignPathWithinRoot(rootPath string, targetPath string) (cleanRootReal string, rel string, err error) {
	if strings.ContainsRune(rootPath, '\x00') || strings.ContainsRune(targetPath, '\x00') {
		return "", "", ErrPathOutsideInstanceRoot
	}
	if hasNonLeadingDotDot(rootPath) || hasNonLeadingDotDot(targetPath) {
		return "", "", ErrPathOutsideInstanceRoot
	}
	if HasWindowsReservedDeviceComponent(targetPath) {
		return "", "", ErrPathOutsideInstanceRoot
	}
	cleanLogical := cleanAbsPath(rootPath)
	cleanTarget := cleanAbsPath(targetPath)
	rootReal, err := filepath.EvalSymlinks(cleanLogical)
	if err != nil {
		return "", "", fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	cleanReal := cleanAbsPath(rootReal)
	if SameCleanPath(cleanTarget, cleanLogical) || SameCleanPath(cleanTarget, cleanReal) {
		return cleanReal, ".", nil
	}
	if r, ok := relativePathWithinBase(cleanLogical, cleanTarget); ok {
		return cleanReal, r, nil
	}
	if r, ok := relativePathWithinBase(cleanReal, cleanTarget); ok {
		return cleanReal, r, nil
	}
	matchedAncestor := ""
	for ancestor := cleanTarget; ancestor != ""; {
		if ancestorReal, evalErr := filepath.EvalSymlinks(ancestor); evalErr == nil {
			if SameCleanPath(ancestorReal, cleanReal) {
				matchedAncestor = ancestor
			}
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		ancestor = parent
	}
	if matchedAncestor != "" {
		if r, ok := relativePathWithinBase(matchedAncestor, cleanTarget); ok {
			return cleanReal, r, nil
		}
	}
	return "", "", ErrPathOutsideInstanceRoot
}

func EnsureResolvedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	rootAbs, err := GetInstanceRootPath(sp)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(cleanAbsPath(rootAbs))
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}
	targetReal, err := filepath.EvalSymlinks(cleanAbsPath(targetPath))
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
	rootReal, err := filepath.EvalSymlinks(cleanAbsPath(rootAbs))
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}

	parent := filepath.Dir(cleanAbsPath(targetPath))
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
	cleanRootReal, rel, err := alignPathWithinRoot(rootPath, targetPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) == 0 {
		return nil
	}
	limit := len(parts)
	if !includeLeaf {
		limit--
	}
	if limit <= 0 {
		return nil
	}
	current := cleanRootReal
	for i := range limit {
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
		if !IsPathWithinRoot(cleanRootReal, realCurrent) {
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
// require fd-based openat/no-follow primitives.
func EnsureDirectoryStepwise(rootPath string, dirPath string, mode os.FileMode) error {
	cleanRootReal, rel, err := alignPathWithinRoot(rootPath, dirPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))

	current := cleanRootReal
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
				return errors.New(msg.PathNotDirectory)
			}
			realCurrent, rerr := filepath.EvalSymlinks(current)
			if rerr != nil {
				return fmt.Errorf(msg.PathInvalidFmt, rerr)
			}
			if !IsPathWithinRoot(cleanRootReal, realCurrent) {
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
			return errors.New(msg.PathNotDirectory)
		}
		realCurrent, rerr := filepath.EvalSymlinks(current)
		if rerr != nil {
			return fmt.Errorf(msg.PathInvalidFmt, rerr)
		}
		if !IsPathWithinRoot(cleanRootReal, realCurrent) {
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
	if name == "." || name == ".." || strings.HasSuffix(name, ".") {
		return "", errors.New(msg.FileNameInvalid)
	}
	if IsWindowsReservedDeviceName(name) {
		return "", errors.New(msg.FileNameInvalid)
	}
	if strings.ContainsAny(name, `\\/:*?"<>|`) {
		return "", errors.New(msg.FileNameInvalidChars)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", errors.New(msg.FileNameInvalidChars)
		}
	}
	return name, nil
}

func IsWindowsReservedDeviceName(name string) bool {
	base := strings.TrimSpace(name)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.ToUpper(strings.TrimSpace(base))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "COM0",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "LPT0":
		return true
	default:
		return false
	}
}
func HasWindowsReservedDeviceComponent(p string) bool {
	for _, part := range strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if IsWindowsReservedDeviceName(part) {
			return true
		}
	}
	return false
}

func textTooLong(value string, maxLen int) bool {
	return utf8.RuneCountInString(value) > maxLen
}
