package process

import (
	compat "IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"

	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	instanceStartPathMu    sync.Mutex
	instanceStartPathLocks = make(map[string]*sync.Mutex)
	instanceUpdateSyncDir  = compat.SyncDirIfPossible
	instanceUpdateRename   = os.Rename
)

func lockInstanceStartPath(path string) func() {
	path = filepath.Clean(strings.TrimSpace(path))
	instanceStartPathMu.Lock()
	mu, ok := instanceStartPathLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		instanceStartPathLocks[path] = mu
	}
	instanceStartPathMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func syncParentDir(path string) error {
	return instanceUpdateSyncDir(filepath.Dir(path))
}

func resolveInstanceUpdateStagingRoot(instanceRoot string, configPath string) (string, error) {
	instanceRoot = strings.TrimSpace(instanceRoot)
	configPath = strings.TrimSpace(configPath)
	if instanceRoot == "" || configPath == "" {
		return "", nil
	}
	if filepath.IsAbs(configPath) {
		return filepath.Clean(configPath), nil
	}
	cleanRel := filepath.Clean(configPath)
	if cleanRel == "." {
		return filepath.Clean(instanceRoot), nil
	}
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(msg.InstanceUpdateStagingDirOutsideRootFmt, configPath)
	}
	return filepath.Clean(filepath.Join(instanceRoot, cleanRel)), nil
}

func isPathWithinRoot(rootPath string, targetPath string) bool {
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

func checkInstanceUpdateCanceled(cancel <-chan struct{}) error {
	if cancel == nil {
		return nil
	}
	select {
	case <-cancel:
		return errors.New(msg.InstanceUpdateCanceled)
	default:
		return nil
	}
}

func validateInstanceUpdateTree(updateRoot string, instanceRoot string, cancel <-chan struct{}) error {
	return filepath.WalkDir(updateRoot, func(current string, d os.DirEntry, walkErr error) error {
		if err := checkInstanceUpdateCanceled(cancel); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(msg.InstanceUpdateStagingDirSymlinkUnsupportedFmt, current)
		}
		if filepath.Clean(current) == filepath.Clean(updateRoot) {
			return nil
		}
		relPath, err := filepath.Rel(updateRoot, current)
		if err != nil {
			return err
		}
		relPath = filepath.Clean(relPath)
		if relPath == "." || relPath == "" {
			return nil
		}
		targetPath := filepath.Clean(filepath.Join(instanceRoot, relPath))
		if !isPathWithinRoot(instanceRoot, targetPath) {
			return fmt.Errorf(msg.InstanceUpdateTargetOutsideRootFmt, relPath)
		}
		if isPathWithinRoot(updateRoot, targetPath) {
			return fmt.Errorf(msg.InstanceUpdateTargetInsideUpdateDirFmt, relPath)
		}
		if isPathWithinRoot(targetPath, updateRoot) {
			return fmt.Errorf(msg.InstanceUpdateTargetContainsUpdateDirFmt, relPath)
		}
		return nil
	})
}

func ApplyStagedInstanceUpdate(rootPath string, updateDir string, cancel <-chan struct{}) error {
	rootPath = strings.TrimSpace(rootPath)
	updateDir = strings.TrimSpace(updateDir)
	updateRoot, err := resolveInstanceUpdateStagingRoot(rootPath, updateDir)
	if err != nil {
		return err
	}
	if updateRoot == "" {
		return nil
	}
	rootPath = filepath.Clean(rootPath)
	updateRoot = filepath.Clean(updateRoot)
	if err := checkInstanceUpdateCanceled(cancel); err != nil {
		return err
	}
	info, err := os.Stat(updateRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf(msg.InstanceUpdateStagingDirNotDirectoryFmt, updateRoot)
	}
	if updateRoot == rootPath {
		return fmt.Errorf(msg.InstanceUpdateStagingDirIsRootFmt, updateRoot)
	}
	if err := validateInstanceUpdateTree(updateRoot, rootPath, cancel); err != nil {
		return err
	}
	entries, err := os.ReadDir(updateRoot)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if err := checkInstanceUpdateCanceled(cancel); err != nil {
			return err
		}
		name := entry.Name()
		srcPath := filepath.Join(updateRoot, name)
		targetPath := filepath.Clean(filepath.Join(rootPath, name))
		if !isPathWithinRoot(rootPath, targetPath) {
			return fmt.Errorf(msg.InstanceUpdateTargetOutsideRootFmt, name)
		}
		if isPathWithinRoot(updateRoot, targetPath) {
			return fmt.Errorf(msg.InstanceUpdateTargetInsideUpdateDirFmt, name)
		}
		if isPathWithinRoot(targetPath, updateRoot) {
			return fmt.Errorf(msg.InstanceUpdateTargetContainsUpdateDirFmt, name)
		}
		if err := os.RemoveAll(targetPath); err != nil {
			return err
		}
		if err := syncParentDir(targetPath); err != nil {
			return err
		}
		if err := checkInstanceUpdateCanceled(cancel); err != nil {
			return err
		}
		if err := instanceUpdateRename(srcPath, targetPath); err != nil {
			return err
		}
		if err := syncParentDir(targetPath); err != nil {
			return err
		}
		if err := syncParentDir(srcPath); err != nil {
			return err
		}
	}
	if err := checkInstanceUpdateCanceled(cancel); err != nil {
		return err
	}
	return instanceUpdateSyncDir(updateRoot)
}
