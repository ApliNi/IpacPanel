package process

import (
	"IpacPanel/controller/src/atomic/file"
	compat "IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"

	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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

type instanceUpdatePlanItem struct {
	name         string
	srcPath      string
	stageRoot    string
	stagePath    string
	targetPath   string
	backupPath   string
	targetExists bool
	committed    bool
	mode         os.FileMode
	isDir        bool
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

func ensureNoSymlinkInTree(path string) error {
	return filepath.WalkDir(path, func(current string, d os.DirEntry, walkErr error) error {
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
		return nil
	})
}

func buildStagedInstanceUpdatePlan(updateRoot string, instanceRoot string) ([]*instanceUpdatePlanItem, error) {
	plan := make([]*instanceUpdatePlanItem, 0)
	err := filepath.WalkDir(updateRoot, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Clean(current) == filepath.Clean(updateRoot) {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(msg.InstanceUpdateStagingDirSymlinkUnsupportedFmt, current)
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
		plan = append(plan, &instanceUpdatePlanItem{
			name:       relPath,
			srcPath:    current,
			targetPath: targetPath,
			mode:       info.Mode(),
			isDir:      info.IsDir(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(plan, func(i int, j int) bool {
		leftDepth := strings.Count(plan[i].name, string(filepath.Separator))
		rightDepth := strings.Count(plan[j].name, string(filepath.Separator))
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return plan[i].name < plan[j].name
	})
	return plan, nil
}

func copyFileWithMode(srcPath string, dstPath string, mode os.FileMode) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	outFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(outFile, srcFile); err != nil {
		_ = outFile.Close()
		return err
	}
	if err := outFile.Sync(); err != nil {
		_ = outFile.Close()
		return err
	}
	if err := outFile.Close(); err != nil {
		return err
	}
	return os.Chmod(dstPath, mode.Perm())
}

func copyTree(srcPath string, dstPath string) error {
	info, err := os.Lstat(srcPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(msg.InstanceUpdateStagingDirSymlinkUnsupportedFmt, srcPath)
	}
	if !info.IsDir() {
		return copyFileWithMode(srcPath, dstPath, info.Mode())
	}
	if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(dstPath, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(srcPath, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Clean(current) == filepath.Clean(srcPath) {
			return nil
		}
		entryInfo, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(msg.InstanceUpdateStagingDirSymlinkUnsupportedFmt, current)
		}
		relPath, err := filepath.Rel(srcPath, current)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dstPath, relPath)
		if d.IsDir() {
			if err := os.MkdirAll(targetPath, entryInfo.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(targetPath, entryInfo.Mode().Perm())
		}
		return copyFileWithMode(current, targetPath, entryInfo.Mode())
	})
}

func rollbackInstanceUpdatePlan(plan []*instanceUpdatePlanItem) error {
	var rollbackErr error
	for i := len(plan) - 1; i >= 0; i-- {
		item := plan[i]
		if item == nil {
			continue
		}
		if item.committed {
			if err := os.RemoveAll(item.targetPath); err != nil && !os.IsNotExist(err) && rollbackErr == nil {
				rollbackErr = err
			}
		}
		if strings.TrimSpace(item.stageRoot) != "" {
			if err := file.RemoveRegisteredTempDir(item.stageRoot); err != nil && !os.IsNotExist(err) && rollbackErr == nil {
				rollbackErr = err
			}
		}
		if item.targetExists {
			if err := os.MkdirAll(filepath.Dir(item.targetPath), 0755); err != nil {
				if rollbackErr == nil {
					rollbackErr = err
				}
				continue
			}
			if err := instanceUpdateRename(item.backupPath, item.targetPath); err != nil && rollbackErr == nil {
				rollbackErr = err
			} else if rollbackErr == nil {
				if err := syncParentDir(item.targetPath); err != nil {
					rollbackErr = err
				}
			}
		}
	}
	return rollbackErr
}

func ApplyStagedInstanceUpdate(rootPath string, updateDir string) error {
	rootPath = strings.TrimSpace(rootPath)
	updateDir = strings.TrimSpace(updateDir)
	updateRoot, err := resolveInstanceUpdateStagingRoot(rootPath, updateDir)
	if err != nil {
		return err
	}
	if updateRoot == "" {
		return nil
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
	if filepath.Clean(updateRoot) == filepath.Clean(rootPath) {
		return fmt.Errorf(msg.InstanceUpdateStagingDirIsRootFmt, updateRoot)
	}
	if err := ensureNoSymlinkInTree(updateRoot); err != nil {
		return err
	}
	plan, err := buildStagedInstanceUpdatePlan(updateRoot, rootPath)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return nil
	}
	backupRoot, err := file.CreateTempDir(rootPath, 0755)
	if err != nil {
		return err
	}
	removeBackupRoot := true
	defer func() {
		if removeBackupRoot {
			_ = file.RemoveRegisteredTempDir(backupRoot)
		}
	}()
	rollbackUpdate := func(cause error) error {
		if rollbackErr := rollbackInstanceUpdatePlan(plan); rollbackErr != nil {
			removeBackupRoot = false
			if unregisterErr := file.UnregisterTempDir(backupRoot); unregisterErr != nil {
				rollbackErr = errors.Join(rollbackErr, unregisterErr)
			}
			return fmt.Errorf(msg.RollbackFailedBackupKeptFmt, backupRoot, errors.Join(cause, rollbackErr))
		}
		return cause
	}
	for _, item := range plan {
		if !item.isDir {
			stageRoot, err := file.CreateTempDir(rootPath, 0755)
			if err != nil {
				return rollbackUpdate(err)
			}
			item.stageRoot = stageRoot
			item.stagePath = filepath.Join(stageRoot, item.name)
			if err := copyFileWithMode(item.srcPath, item.stagePath, item.mode); err != nil {
				return rollbackUpdate(err)
			}
			if err := syncParentDir(item.stagePath); err != nil {
				return rollbackUpdate(err)
			}
		}
		item.backupPath = filepath.Join(backupRoot, item.name)
		if targetInfo, err := os.Lstat(item.targetPath); err == nil {
			if item.isDir && targetInfo.IsDir() {
				continue
			}
			item.targetExists = true
			if err := os.MkdirAll(filepath.Dir(item.backupPath), 0755); err != nil {
				return rollbackUpdate(err)
			}
			if err := instanceUpdateRename(item.targetPath, item.backupPath); err != nil {
				return rollbackUpdate(err)
			}
			if err := syncParentDir(item.targetPath); err != nil {
				return rollbackUpdate(err)
			}
			continue
		} else if !os.IsNotExist(err) {
			return rollbackUpdate(err)
		}
	}
	for _, item := range plan {
		if item.isDir {
			if _, err := os.Lstat(item.targetPath); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return rollbackUpdate(err)
			}
			if err := os.MkdirAll(item.targetPath, item.mode.Perm()); err != nil {
				return rollbackUpdate(err)
			}
			if err := os.Chmod(item.targetPath, item.mode.Perm()); err != nil {
				return rollbackUpdate(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(item.targetPath), 0755); err != nil {
				return rollbackUpdate(err)
			}
			if err := instanceUpdateRename(item.stagePath, item.targetPath); err != nil {
				return rollbackUpdate(err)
			}
			item.stagePath = ""
		}
		item.committed = true
		if strings.TrimSpace(item.stageRoot) != "" {
			if err := file.RemoveRegisteredTempDir(item.stageRoot); err != nil {
				log.Printf(msg.InstanceUpdateStageCleanupFailedLogFmt, item.stageRoot, err)
			}
		}
		item.stageRoot = ""
		if err := syncParentDir(item.targetPath); err != nil {
			return rollbackUpdate(err)
		}
	}
	if err := removeInstanceUpdateContents(updateRoot); err != nil {
		return rollbackUpdate(err)
	}
	return nil
}

func removeInstanceUpdateContents(updateRoot string) error {
	entries, err := os.ReadDir(updateRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(updateRoot, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
