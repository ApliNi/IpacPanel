package file

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"IpacPanel/controller/src/compat"

	"gopkg.in/yaml.v3"
)

const tempPrefix = ".IpacAtomicFile"

var (
	tempSequence uint64
	registryMu   sync.Mutex
	registryPath string
)

type tempDirRegistry struct {
	AtomicDirs []tempDirRegistryEntry `yaml:"atomic_dirs"`
}

type tempDirRegistryEntry struct {
	Path string `yaml:"path"`
}

type Options struct {
	Overwrite bool
	Mode      os.FileMode
	SyncDir   bool
}

type DirOptions struct {
	Overwrite bool
	SyncDir   bool
}

func TempName() string {
	return tempPrefix + "-" + nextSequenceSuffix()
}

func SetRegistryPath(path string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryPath = cleanRegistryPath(path)
}

func CreateTempDir(parent string, mode os.FileMode) (string, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		parent = "."
	}
	registryMu.Lock()
	configured := registryPath != ""
	registryMu.Unlock()
	if !configured {
		return "", errors.New("atomic temp directory registry path is not set")
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", err
	}
	for {
		tempDir := filepath.Join(parent, TempName())
		if err := os.Mkdir(tempDir, effectiveDirMode(mode)); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		if err := RegisterTempDir(tempDir); err != nil {
			removeErr := os.RemoveAll(tempDir)
			return "", errors.Join(err, removeErr)
		}
		return tempDir, nil
	}
}

func RegisterTempDir(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New("temp directory path is empty")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempDirRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	for _, entry := range registry.AtomicDirs {
		if cleanRegistryPath(entry.Path) == path {
			return writeTempDirRegistryLocked(registryFile, registry)
		}
	}
	registry.AtomicDirs = append(registry.AtomicDirs, tempDirRegistryEntry{Path: path})
	return writeTempDirRegistryLocked(registryFile, registry)
}

func UnregisterTempDir(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New("temp directory path is empty")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempDirRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	registry.AtomicDirs = filterTempDirRegistryEntries(registry.AtomicDirs, path)
	return writeTempDirRegistryLocked(registryFile, registry)
}

func RemoveRegisteredTempDir(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New("temp directory path is empty")
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return UnregisterTempDir(path)
}

func CleanupRegisteredAtomicTempDirs() error {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempDirRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	remaining := make([]tempDirRegistryEntry, 0, len(registry.AtomicDirs))
	var cleanupErr error
	for _, entry := range registry.AtomicDirs {
		path := cleanRegistryPath(entry.Path)
		if path == "" {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			remaining = append(remaining, tempDirRegistryEntry{Path: path})
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
	}
	registry.AtomicDirs = remaining
	writeErr := writeTempDirRegistryLocked(registryFile, registry)
	return errors.Join(cleanupErr, writeErr)
}

func OpenTempForTarget(targetPath string, mode os.FileMode) (*os.File, string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, "", errors.New("target path is empty")
	}
	dir := filepath.Dir(targetPath)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}
	for {
		file, err := os.OpenFile(filepath.Join(dir, TempName()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, effectiveMode(mode))
		if err == nil {
			return file, file.Name(), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
}

func CommitTemp(tempPath string, targetPath string, overwrite bool, syncDir bool) error {
	tempPath = strings.TrimSpace(tempPath)
	targetPath = strings.TrimSpace(targetPath)
	if tempPath == "" || targetPath == "" {
		return errors.New("temp path and target path are required")
	}
	if overwrite {
		if err := compat.ReplaceFileAtomic(tempPath, targetPath); err != nil {
			if os.IsNotExist(err) {
				if renameErr := compat.RenameNoReplace(tempPath, targetPath); renameErr != nil {
					return renameErr
				}
			} else {
				return err
			}
		}
	} else {
		if err := compat.RenameNoReplace(tempPath, targetPath); err != nil {
			return err
		}
	}
	if syncDir {
		return SyncDir(filepath.Dir(targetPath))
	}
	return nil
}

func CommitTempDir(tempDir string, targetPath string, options DirOptions) error {
	tempDir = strings.TrimSpace(tempDir)
	targetPath = strings.TrimSpace(targetPath)
	if tempDir == "" || targetPath == "" {
		return errors.New("temp directory and target path are required")
	}
	parent := filepath.Dir(targetPath)
	if parent == "" {
		parent = "."
	}
	if !options.Overwrite {
		if err := compat.RenameNoReplace(tempDir, targetPath); err != nil {
			return err
		}
		if options.SyncDir {
			return SyncDir(parent)
		}
		return nil
	}

	backupPath := filepath.Join(parent, TempName()+"-backup")
	targetExisted := false
	if _, err := os.Lstat(targetPath); err == nil {
		targetExisted = true
		if err := compat.RenameNoReplace(targetPath, backupPath); err != nil {
			return err
		}
		if options.SyncDir {
			if err := SyncDir(parent); err != nil {
				_ = os.Rename(backupPath, targetPath)
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := compat.RenameNoReplace(tempDir, targetPath); err != nil {
		if targetExisted {
			_ = os.Rename(backupPath, targetPath)
		}
		return err
	}
	if options.SyncDir {
		if err := SyncDir(parent); err != nil {
			return err
		}
	}
	if targetExisted {
		if err := os.RemoveAll(backupPath); err != nil {
			return err
		}
		if options.SyncDir {
			return SyncDir(parent)
		}
	}
	return nil
}

func WriteFile(path string, data []byte, options Options) error {
	tmp, tmpPath, err := OpenTempForTarget(path, options.Mode)
	if err != nil {
		return err
	}
	committed := false
	defer cleanupTemp(tmpPath, &committed)

	if len(data) > 0 {
		if _, err := tmp.Write(data); err != nil {
			closeErr := tmp.Close()
			return errors.Join(err, closeErr)
		}
	}
	if err := tmp.Chmod(effectiveMode(options.Mode)); err != nil {
		closeErr := tmp.Close()
		return errors.Join(err, closeErr)
	}
	if err := tmp.Sync(); err != nil {
		closeErr := tmp.Close()
		return errors.Join(err, closeErr)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := CommitTemp(tmpPath, path, options.Overwrite, options.SyncDir); err != nil {
		return err
	}
	committed = true
	return nil
}

func CopyFile(src string, dst string, options Options) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, tmpPath, err := OpenTempForTarget(dst, options.Mode)
	if err != nil {
		return err
	}
	committed := false
	defer cleanupTemp(tmpPath, &committed)

	if _, err := io.Copy(tmp, in); err != nil {
		closeErr := tmp.Close()
		return errors.Join(err, closeErr)
	}
	if err := tmp.Chmod(effectiveMode(options.Mode)); err != nil {
		closeErr := tmp.Close()
		return errors.Join(err, closeErr)
	}
	if err := tmp.Sync(); err != nil {
		closeErr := tmp.Close()
		return errors.Join(err, closeErr)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := CommitTemp(tmpPath, dst, options.Overwrite, options.SyncDir); err != nil {
		return err
	}
	committed = true
	return nil
}

func SyncDir(path string) error {
	return compat.SyncDirIfPossible(path)
}

func effectiveMode(mode os.FileMode) os.FileMode {
	if mode == 0 {
		return 0644
	}
	return mode.Perm()
}

func effectiveDirMode(mode os.FileMode) os.FileMode {
	if mode == 0 {
		return 0755
	}
	return mode.Perm()
}

func requireRegistryPathLocked() (string, error) {
	if registryPath == "" {
		return "", errors.New("atomic temp directory registry path is not set")
	}
	return registryPath, nil
}

func cleanRegistryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func loadTempDirRegistryLocked(path string) (tempDirRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tempDirRegistry{}, nil
		}
		return tempDirRegistry{}, err
	}
	var registry tempDirRegistry
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return tempDirRegistry{}, err
	}
	registry.AtomicDirs = normalizeTempDirRegistryEntries(registry.AtomicDirs)
	return registry, nil
}

func writeTempDirRegistryLocked(path string, registry tempDirRegistry) error {
	registry.AtomicDirs = normalizeTempDirRegistryEntries(registry.AtomicDirs)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(registry); err != nil {
		_ = enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return WriteFile(path, buf.Bytes(), Options{Overwrite: true, Mode: 0644, SyncDir: true})
}

func normalizeTempDirRegistryEntries(entries []tempDirRegistryEntry) []tempDirRegistryEntry {
	seen := make(map[string]struct{}, len(entries))
	normalized := make([]tempDirRegistryEntry, 0, len(entries))
	for _, entry := range entries {
		path := cleanRegistryPath(entry.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, tempDirRegistryEntry{Path: path})
	}
	return normalized
}

func filterTempDirRegistryEntries(entries []tempDirRegistryEntry, removePath string) []tempDirRegistryEntry {
	removePath = cleanRegistryPath(removePath)
	filtered := make([]tempDirRegistryEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		path := cleanRegistryPath(entry.Path)
		if path == "" || path == removePath {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		filtered = append(filtered, tempDirRegistryEntry{Path: path})
	}
	return filtered
}

func cleanupTemp(path string, committed *bool) {
	if committed != nil && *committed {
		return
	}
	if strings.TrimSpace(path) != "" {
		_ = os.Remove(path)
	}
}

func nextSequenceSuffix() string {
	seq := atomic.AddUint64(&tempSequence, 1)
	return strconv.FormatUint(seq, 10)
}
