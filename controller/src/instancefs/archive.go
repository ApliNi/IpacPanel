package instancefs

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type ArchiveRule struct {
	Path  string
	IsDir bool
}

func (fs *InstanceFS) ResolveArchiveRules(rules []ArchiveRule, requireExisting bool) ([]ArchiveRule, error) {
	resolved := make([]ArchiveRule, 0, len(rules))
	for _, rule := range rules {
		safePath, err := fs.ResolveRequired(rule.Path)
		if err != nil {
			return nil, err
		}
		absPath := safePath.AbsPath()
		if requireExisting {
			if err := EnsurePathComponentsWithinRoot(fs.rootPath, absPath, false); err != nil {
				return nil, err
			}
			info, err := os.Lstat(absPath)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if err := fs.ensureResolvedPathWithinRoot(absPath); err != nil {
				return nil, err
			}
			resolved = append(resolved, ArchiveRule{Path: filepath.Clean(absPath), IsDir: info.IsDir()})
			continue
		}
		cleanRoot := filepath.Clean(fs.rootPath)
		cleanPath := filepath.Clean(absPath)
		if !IsPathWithinRoot(cleanRoot, cleanPath) {
			return nil, ErrPathOutsideInstanceRoot
		}
		resolved = append(resolved, ArchiveRule{Path: cleanPath, IsDir: rule.IsDir})
	}
	return resolved, nil
}

func (fs *InstanceFS) ResolveArchiveLayout(instanceName string, include []ArchiveRule) (string, string, error) {
	if len(include) == 0 {
		return "", "", errors.New(msg.FilePathRequired)
	}
	if len(include) == 1 && include[0].IsDir {
		base := filepath.Dir(include[0].Path)
		return base, SafeArchiveDownloadName(filepath.Base(include[0].Path)), nil
	}
	commonParent := filepath.Dir(include[0].Path)
	for _, rule := range include[1:] {
		commonParent = commonArchiveParent(commonParent, filepath.Dir(rule.Path))
	}
	rootClean := filepath.Clean(fs.rootPath)
	if filepath.Clean(commonParent) == rootClean {
		return commonParent, SafeArchiveDownloadName(instanceName), nil
	}
	return commonParent, SafeArchiveDownloadName(filepath.Base(commonParent)), nil
}

func (fs *InstanceFS) EnsureArchiveFileWithinRoot(rootReal string, filePath string) error {
	return EnsureArchiveFileWithinRootStatic(rootReal, filePath)
}

func EnsureArchiveFileWithinRootStatic(rootReal string, filePath string) error {
	realPath, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return err
	}
	if !IsPathWithinRoot(rootReal, realPath) {
		return ErrPathOutsideInstanceRoot
	}
	return nil
}

func (fs *InstanceFS) SafeArchiveEntryName(basePath string, targetPath string) (string, bool) {
	return SafeArchiveEntryName(basePath, targetPath)
}

func commonArchiveParent(a string, b string) string {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	for {
		if IsPathWithinRoot(a, b) {
			return a
		}
		parent := filepath.Dir(a)
		if parent == a {
			return parent
		}
		a = parent
	}
}

func SafeArchiveDownloadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" || name == "." || name == ".." {
		name = "archive"
	}
	return name + ".zip"
}

func SafeArchiveEntryName(basePath string, targetPath string) (string, bool) {
	if filepath.VolumeName(targetPath) != filepath.VolumeName(basePath) {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(basePath), filepath.Clean(targetPath))
	if err != nil || rel == "." || rel == "" {
		return "", false
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	name := filepath.ToSlash(rel)
	if strings.Contains(name, "\x00") || strings.HasPrefix(name, "/") {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || filepath.VolumeName(part) != "" {
			return "", false
		}
	}
	return name, true
}
