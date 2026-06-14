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
	"time"

	"IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"

	"gopkg.in/yaml.v3"
)

const tempPrefix = ".IpacAtomicFile"

var (
	tempSequence uint64
	registryMu   sync.Mutex
	registryPath string
)

type tempRegistry struct {
	AtomicTemps []string `yaml:"atomic_temps"`
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
	return CreateRegisteredTempDir(parent, mode)
}

func CreateRegisteredTempDir(parent string, mode os.FileMode) (string, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		parent = "."
	}
	registryMu.Lock()
	configured := registryPath != ""
	registryMu.Unlock()
	if !configured {
		return "", errors.New(msg.AtomicTempRegistryPathNotSet)
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
		if err := RegisterTempPath(tempDir); err != nil {
			removeErr := os.RemoveAll(tempDir)
			return "", errors.Join(err, removeErr)
		}
		return tempDir, nil
	}
}

func RegisterTempDir(path string) error {
	return RegisterTempPath(path)
}

func RegisterTempPath(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New(msg.TempDirectoryPathEmpty)
	}
	if !IsAtomicTempRegistryPath(path) {
		return nil
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	for _, registeredPath := range registry.AtomicTemps {
		if cleanRegistryPath(registeredPath) == path {
			return writeTempRegistryLocked(registryFile, registry)
		}
	}
	registry.AtomicTemps = append(registry.AtomicTemps, path)
	return writeTempRegistryLocked(registryFile, registry)
}

func UnregisterTempDir(path string) error {
	return UnregisterTempPath(path)
}

func UnregisterTempPath(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New(msg.TempDirectoryPathEmpty)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	registry.AtomicTemps = filterTempRegistryPaths(registry.AtomicTemps, path)
	return writeTempRegistryLocked(registryFile, registry)
}

func RemoveRegisteredTempDir(path string) error {
	return RemoveRegisteredTempPath(path)
}

func RemoveRegisteredTempPath(path string) error {
	path = cleanRegistryPath(path)
	if path == "" {
		return errors.New(msg.TempDirectoryPathEmpty)
	}
	if !IsAtomicTempRegistryPath(path) {
		return UnregisterTempPath(path)
	}
	if err := removeAtomicTempPath(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return UnregisterTempPath(path)
}

func CleanupRegisteredAtomicTempDirs() error {
	return CleanupRegisteredAtomicTemps()
}

func CleanupRegisteredAtomicTemps() error {
	registryMu.Lock()
	defer registryMu.Unlock()
	registryFile, err := requireRegistryPathLocked()
	if err != nil {
		return err
	}
	registry, err := loadTempRegistryLocked(registryFile)
	if err != nil {
		return err
	}
	remaining := make([]string, 0, len(registry.AtomicTemps))
	var cleanupErr error
	for _, registeredPath := range registry.AtomicTemps {
		path := cleanRegistryPath(registeredPath)
		if path == "" || !IsAtomicTempRegistryPath(path) {
			continue
		}
		if err := removeAtomicTempPath(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			remaining = append(remaining, path)
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
	}
	registry.AtomicTemps = remaining
	writeErr := writeTempRegistryLocked(registryFile, registry)
	return errors.Join(cleanupErr, writeErr)
}

func OpenTempForTarget(targetPath string, mode os.FileMode) (*os.File, string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, "", errors.New(msg.TargetPathEmpty)
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

func CreateRegisteredTempFileForTarget(targetPath string, mode os.FileMode) (*os.File, string, error) {
	tmp, tmpPath, err := OpenTempForTarget(targetPath, mode)
	if err != nil {
		return nil, "", err
	}
	if err := RegisterTempPath(tmpPath); err != nil {
		closeErr := tmp.Close()
		removeErr := os.Remove(tmpPath)
		return nil, "", errors.Join(err, closeErr, removeErr)
	}
	return tmp, tmpPath, nil
}

func CommitTemp(tempPath string, targetPath string, overwrite bool, syncDir bool) error {
	tempPath = strings.TrimSpace(tempPath)
	targetPath = strings.TrimSpace(targetPath)
	if tempPath == "" || targetPath == "" {
		return errors.New(msg.TempPathAndTargetPathRequired)
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
		return errors.New(msg.TempDirectoryAndTargetPathRequired)
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
		return "", errors.New(msg.AtomicTempRegistryPathNotSet)
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

func loadTempRegistryLocked(path string) (tempRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tempRegistry{}, nil
		}
		return tempRegistry{}, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		backupPath := nextCorruptBackupPath(path)
		if renameErr := os.Rename(path, backupPath); renameErr != nil {
			return tempRegistry{}, renameErr
		}
		return tempRegistry{}, nil
	}
	registry := parseTempRegistryNode(&root)
	registry.AtomicTemps = normalizeTempRegistryPaths(registry.AtomicTemps)
	return registry, nil
}

func writeTempRegistryLocked(path string, registry tempRegistry) error {
	registry.AtomicTemps = normalizeTempRegistryPaths(registry.AtomicTemps)
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

func parseTempRegistryNode(root *yaml.Node) tempRegistry {
	if root == nil {
		return tempRegistry{}
	}
	node := root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return tempRegistry{}
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return tempRegistry{}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Value != "atomic_temps" || value.Kind != yaml.SequenceNode {
			continue
		}
		paths := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			paths = append(paths, item.Value)
		}
		return tempRegistry{AtomicTemps: paths}
	}
	return tempRegistry{}
}

func normalizeTempRegistryPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, registeredPath := range paths {
		path := cleanRegistryPath(registeredPath)
		if path == "" || !IsAtomicTempRegistryPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	return normalized
}

func filterTempRegistryPaths(paths []string, removePath string) []string {
	removePath = cleanRegistryPath(removePath)
	filtered := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, registeredPath := range paths {
		path := cleanRegistryPath(registeredPath)
		if path == "" || path == removePath || !IsAtomicTempRegistryPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		filtered = append(filtered, path)
	}
	return filtered
}

func IsAtomicTempRegistryPath(path string) bool {
	base := filepath.Base(cleanRegistryPath(path))
	matched, err := filepath.Match(tempPrefix+"-*", base)
	return err == nil && matched
}

func removeAtomicTempPath(path string) error {
	path = cleanRegistryPath(path)
	if path == "" || !IsAtomicTempRegistryPath(path) {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func nextCorruptBackupPath(path string) string {
	base := path + ".corrupt." + time.Now().Format("20060102150405")
	backupPath := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(backupPath); errors.Is(err, os.ErrNotExist) {
			return backupPath
		}
		backupPath = base + "." + strconv.Itoa(i)
	}
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
