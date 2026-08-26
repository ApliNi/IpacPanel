package api

import (
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	process "IpacPanel/controller/src/process"
	"IpacPanel/controller/src/web/authz"

	web "IpacPanel/controller/src/web"

	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type fileBatchExcludeMatcher struct {
	dirs  []string
	files map[string]struct{}
}

func (m fileBatchExcludeMatcher) empty() bool {
	return len(m.dirs) == 0 && len(m.files) == 0
}

func cleanAndEnsureDirSlash(p string) string {
	pp := strings.TrimSpace(p)
	if pp == "" {
		return ""
	}
	pp = cleanFileBatchMatcherPath(pp)
	sep := string(filepath.Separator)
	if !strings.HasSuffix(pp, sep) {
		pp += sep
	}
	return pp
}

func cleanFileBatchMatcherPath(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

func isSameOrSubDir(parentDir string, candidateDir string) bool {
	parent := cleanAndEnsureDirSlash(parentDir)
	cand := cleanAndEnsureDirSlash(candidateDir)
	if parent == "" || cand == "" {
		return false
	}
	return strings.HasPrefix(cand, parent)
}

func newFileBatchExcludeMatcherFromAbsoluteRules(exclude []fileBatchRule) fileBatchExcludeMatcher {
	matcher := fileBatchExcludeMatcher{files: make(map[string]struct{})}
	for _, r := range exclude {
		rp := strings.TrimSpace(r.Path)
		if rp == "" {
			continue
		}
		rp = cleanFileBatchMatcherPath(rp)
		if r.IsDir {
			matcher.dirs = append(matcher.dirs, cleanAndEnsureDirSlash(rp))
			continue
		}
		matcher.files[cleanFileBatchMatcherPath(rp)] = struct{}{}
	}
	return matcher
}

func resolveFileBatchExcludeAbsolutePath(sp *process.InstanceProcess, rootPath string, rulePath string) (string, error) {
	rulePath = strings.TrimSpace(rulePath)
	if rulePath == "" {
		return "", errors.New(msg.FilePathRequired)
	}
	if textTooLong(rulePath, maxFilePathTextLen) {
		return "", errors.New(msg.PathTooLong)
	}

	osPath := strings.ReplaceAll(rulePath, "\\", string(filepath.Separator))
	osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
	if !filepath.IsAbs(osPath) {
		_, rel, err := resolveInstanceFilePath(sp, rulePath)
		if err != nil {
			return "", err
		}
		excludeAbs := rootPath
		if rel != "" {
			excludeAbs = filepath.Join(rootPath, filepath.FromSlash(rel))
		}
		if err := ensurePathComponentsWithinRoot(rootPath, excludeAbs, false); err != nil {
			return "", err
		}
		return filepath.Clean(excludeAbs), nil
	}

	excludeAbs := filepath.Clean(osPath)
	if !filepath.IsAbs(excludeAbs) {
		return "", errors.New(msg.PathOutsideInstanceRoot)
	}
	if !isCleanPathWithinRoot(rootPath, excludeAbs) {
		return "", errors.New(msg.PathOutsideInstanceRoot)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, excludeAbs, true); err != nil {
		return "", err
	}
	return excludeAbs, nil
}

func isCleanPathWithinRoot(rootPath string, targetPath string) bool {
	root := filepath.Clean(rootPath)
	target := filepath.Clean(targetPath)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		target = strings.ToLower(target)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func resolveFileBatchExcludeMatcher(sp *process.InstanceProcess, rootPath string, exclude []fileBatchRule) (fileBatchExcludeMatcher, error) {
	resolved := make([]fileBatchRule, 0, len(exclude))
	for _, rule := range exclude {
		excludeAbs, err := resolveFileBatchExcludeAbsolutePath(sp, rootPath, rule.Path)
		if err != nil {
			return fileBatchExcludeMatcher{}, err
		}
		resolved = append(resolved, fileBatchRule{Path: excludeAbs, IsDir: rule.IsDir})
	}
	return newFileBatchExcludeMatcherFromAbsoluteRules(resolved), nil
}

func (m fileBatchExcludeMatcher) excludes(path string, isDir bool) bool {
	if path == "" {
		return false
	}
	clean := cleanFileBatchMatcherPath(path)
	if isDir {
		clean = cleanAndEnsureDirSlash(clean)
	}
	for _, rp := range m.dirs {
		if strings.HasPrefix(clean, rp) {
			return true
		}
	}
	_, ok := m.files[cleanFileBatchMatcherPath(clean)]
	if ok {
		return true
	}
	return false
}

func copyDuplicatePath(path string, index int) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		name = base
		ext = ""
	}
	return filepath.Join(dir, name+"_"+strconv.Itoa(index)+ext)
}

func resolveCopyFileDestination(path string, createDuplicate bool) (string, error) {
	if !createDuplicate {
		return path, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	for i := 1; i <= 10000; i++ {
		candidate := copyDuplicatePath(path, i)
		if _, err := os.Stat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", errors.New(msg.TargetAlreadyExists)
}

func resolveCopyDirectoryDestination(path string, createDuplicate bool) (string, error) {
	if !createDuplicate {
		return path, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	for i := 1; i <= 10000; i++ {
		candidate := copyDuplicatePath(path, i)
		if _, err := os.Stat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", errors.New(msg.TargetAlreadyExists)
}

func resolveFileBatchActionDestination(rootPath string, destAbs string, srcAbs string, isDir bool, action string, copyDuplicate bool) (string, error) {
	dstAbs := destAbs
	if action != "copy" && action != "move" {
		return dstAbs, nil
	}

	dstAbs = filepath.Join(destAbs, filepath.Base(srcAbs))
	if action == "copy" && copyDuplicate {
		var err error
		if isDir {
			dstAbs, err = resolveCopyDirectoryDestination(dstAbs, true)
		} else {
			dstAbs, err = resolveCopyFileDestination(dstAbs, true)
		}
		if err != nil {
			return "", err
		}
	}

	if err := ensurePathComponentsWithinRoot(rootPath, dstAbs, false); err != nil {
		return "", err
	}

	// Prevent copying/moving onto itself after copy_duplicate has had a chance to pick name_1/name_2.
	if filepath.Clean(srcAbs) == filepath.Clean(dstAbs) {
		return "", errors.New(msg.TargetSameAsSource)
	}

	// Apply directory subdir protection after duplicate destination is resolved.
	// In-place duplicate directory copy must target a sibling copy (name_1), not a child of source.
	if isDir && isSameOrSubDir(srcAbs, dstAbs) {
		return "", errors.New(msg.TargetDirectoryInsideSource)
	}

	return dstAbs, nil
}

func fileBatchMoveFailReason(err error) string {
	var partialErr *moveFileCopiedRemoveSourceError
	if errors.As(err, &partialErr) {
		return partialErr.Error()
	}
	return err.Error()
}

func removeEmptyDirectoryTreeWithinRoot(ctx context.Context, rootPath string, targetPath string) error {
	dirs := make([]string, 0)
	if err := filepath.WalkDir(targetPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensurePathComponentsWithinRoot(rootPath, path, true); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(dirs[i])
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if len(entries) > 0 {
			continue
		}
		if err := removeEmptyDirectoryWithinRoot(rootPath, dirs[i]); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func removeMovedDirectorySourceWithinRoot(ctx context.Context, rootPath string, targetPath string, hasExcludeRules bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if hasExcludeRules {
		return removeEmptyDirectoryTreeWithinRoot(ctx, rootPath, targetPath)
	}
	return removeDirectoryWithinRootRespectingExcludes(ctx, rootPath, targetPath, fileBatchExcludeMatcher{})
}

func removeDirectoryWithinRootRespectingExcludes(ctx context.Context, rootPath string, targetPath string, excludes fileBatchExcludeMatcher) error {
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}

	type deleteEntry struct {
		path  string
		isDir bool
	}
	entries := make([]deleteEntry, 0)
	partial := &instancefs.PartialDeleteError{}

	if err := filepath.WalkDir(targetPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			isDir := entry != nil && entry.IsDir()
			partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, path), IsDir: isDir, Reason: walkErr})
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensurePathComponentsWithinRoot(rootPath, path, true); err != nil {
			partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, path), IsDir: entry.IsDir(), Reason: err})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, path), IsDir: entry.IsDir(), Reason: errors.New(msg.PathOutsideInstanceRoot)})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		isDir := entry.IsDir()
		if path != targetPath && excludes.excludes(path, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}

		entries = append(entries, deleteEntry{path: path, isDir: isDir})
		return nil
	}); err != nil {
		return err
	}

	for i := len(entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := entries[i]
		if entry.isDir {
			dirEntries, err := os.ReadDir(entry.path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, entry.path), IsDir: true, Reason: err})
				continue
			}
			if len(dirEntries) > 0 && excludes.empty() {
				partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, entry.path), IsDir: true, Reason: errors.New(msg.PartialDeleteFailed)})
				continue
			}
			if len(dirEntries) > 0 {
				continue
			}
			if err := removeEmptyDirectoryWithinRoot(rootPath, entry.path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, entry.path), IsDir: true, Reason: err})
			}
			continue
		}
		if err := removeFileWithinRoot(rootPath, entry.path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			partial.Failures = append(partial.Failures, instancefs.DeleteFailure{Path: instancefs.RelativeDeleteFailurePath(rootPath, entry.path), Reason: err})
		}
	}
	if len(partial.Failures) > 0 {
		return partial
	}
	return nil
}

func failPartialDelete(fail func(string, string, bool), fallbackPath string, fallbackIsDir bool, err error) {
	var partialErr *instancefs.PartialDeleteError
	if !errors.As(err, &partialErr) || partialErr == nil || len(partialErr.Failures) == 0 {
		fail(fallbackPath, deleteFailureReason(err), fallbackIsDir)
		return
	}
	for _, failure := range partialErr.Failures {
		failurePath := failure.Path
		if strings.TrimSpace(failurePath) == "" {
			failurePath = fallbackPath
		}
		fail(failurePath, deleteFailureReason(failure.Reason), failure.IsDir)
	}
}

func HandleApiFileBatch(w http.ResponseWriter, r *http.Request) {
	var req fileBatchActionRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, req.Instance)
	if !ok {
		return
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "copy" && action != "move" && action != "delete" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidOperation, nil)
		return
	}
	web.MarkRequestAction(w, action)

	// Resolve destination directory (copy/move use current file list directory).
	destRoot, destRel, err := resolveInstanceFilePath(sp, req.DestDir)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}
	destAbs := destRoot
	rootPath := destRoot
	if destRel != "" {
		destAbs = filepath.Join(destRoot, filepath.FromSlash(destRel))
	}
	if destAbs == "" {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.TargetPathEmpty))
		return
	}
	if action != "delete" {
		info, err := os.Stat(destAbs)
		if err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		if !info.IsDir() {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.DestinationNotDirectory))
			return
		}
		if err := ensureResolvedPathWithinInstanceRoot(sp, destAbs); err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
		if err := ensurePathComponentsWithinRoot(rootPath, destAbs, true); err != nil {
			web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
			return
		}
	}
	excludes, err := resolveFileBatchExcludeMatcher(sp, rootPath, req.Exclude)
	if err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, err)
		return
	}

	// SSE-like response.
	sse, ok := web.BeginSSE(w)
	if !ok {
		return
	}
	web.LogWebAccess(w, r, http.StatusOK)

	var sseFailed atomic.Bool
	var sseErr atomic.Value // stores error
	reportSseErr := func(err error) {
		if err == nil {
			return
		}
		if sseFailed.CompareAndSwap(false, true) {
			sseErr.Store(err)
		}
	}
	getSseErr := func() error {
		if !sseFailed.Load() {
			return nil
		}
		v := sseErr.Load()
		if v == nil {
			return errors.New(msg.SSEWriteFailed)
		}
		if e, ok := v.(error); ok {
			return e
		}
		return errors.New(msg.SSEWriteFailed)
	}
	const sseCheckInterval = 64
	checkSseOrCanceled := func(iter int) bool {
		// Keep this cheap: only check on a schedule.
		if iter%sseCheckInterval != 0 {
			return true
		}
		select {
		case <-r.Context().Done():
			return false
		default:
		}
		return !sseFailed.Load()
	}
	logSseFailureOnce := func() {
		err := getSseErr()
		if err != nil {
			web.MarkAPIError(w, http.StatusInternalServerError, msg.SSEWriteFailed, err)
		}
	}

	keepAliveStop := make(chan struct{})
	defer close(keepAliveStop)
	go func() {
		t := time.NewTicker(sseKeepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := sse.SendComment(); err != nil {
					reportSseErr(err)
					return
				}
			case <-r.Context().Done():
				return
			case <-keepAliveStop:
				return
			}
		}
	}()

	okCount := 0
	failCount := 0
	lastProgressAt := time.Time{}
	minProgressInterval := sseProgressThrottleInterval

	sendProgress := func(force bool) {
		if sseFailed.Load() {
			return
		}
		now := time.Now()
		if !force {
			if !lastProgressAt.IsZero() && now.Sub(lastProgressAt) < minProgressInterval {
				return
			}
		}
		lastProgressAt = now
		reportSseErr(sse.SendEvent("progress", map[string]int{"ok": okCount, "fail": failCount}))
	}

	fail := func(path string, reason string, isDir bool) {
		if sseFailed.Load() {
			return
		}
		failCount += 1
		reportSseErr(sse.SendEvent("fail", map[string]interface{}{"path": path, "reason": reason, "is_dir": isDir}))
		sendProgress(false)
	}
	success := func() {
		if sseFailed.Load() {
			return
		}
		okCount += 1
		sendProgress(false)
	}

	iter := 0
	for _, rule := range req.Include {
		iter++
		select {
		case <-r.Context().Done():
			return
		default:
		}
		if sseFailed.Load() {
			logSseFailureOnce()
			return
		}

		srcRoot, srcRel, err := resolveInstanceFilePath(sp, rule.Path)
		if err != nil {
			fail(rule.Path, msg.FilePathInvalid, rule.IsDir)
			continue
		}
		if srcRel == "" {
			fail(rule.Path, msg.FilePathRequired, rule.IsDir)
			continue
		}
		srcAbs := filepath.Join(srcRoot, filepath.FromSlash(srcRel))
		if err := ensureResolvedPathWithinInstanceRoot(sp, srcAbs); err != nil {
			fail(rule.Path, msg.FilePathInvalid, rule.IsDir)
			continue
		}
		info, err := os.Lstat(srcAbs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fail(rule.Path, msg.TargetNotFound, rule.IsDir)
			} else {
				fail(rule.Path, deleteFailureReason(err), rule.IsDir)
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			fail(rule.Path, msg.PathOutsideInstanceRoot, rule.IsDir)
			continue
		}

		isDir := info.IsDir()
		dstAbs := destAbs
		if action == "copy" || action == "move" {
			dstAbs, err = resolveFileBatchActionDestination(rootPath, destAbs, srcAbs, isDir, action, req.CopyDuplicate)
			if err != nil {
				fail(rule.Path, err.Error(), isDir)
				continue
			}
		}

		if action == "delete" {
			if excludes.excludes(srcAbs, isDir) {
				continue
			}
			if isDir {
				if err := removeDirectoryWithinRootRespectingExcludes(r.Context(), rootPath, srcAbs, excludes); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return
					}
					failPartialDelete(fail, rule.Path, true, err)
					continue
				}
				success()
				continue
			}
			if err := removeFileWithinRoot(rootPath, srcAbs); err != nil {
				fail(rule.Path, deleteFailureReason(err), false)
				continue
			}
			success()
			continue
		}

		if isDir {
			// Always ensure destination directory exists, even if it ends up empty
			// (e.g. everything inside is excluded).
			if err := ensureDirectoryWithinRoot(rootPath, dstAbs); err != nil {
				fail(rule.Path, err.Error(), true)
				continue
			}

			// Merge directories.
			dirFailCountBeforeMove := failCount
			err = filepath.WalkDir(srcAbs, func(p string, d os.DirEntry, walkErr error) error {
				iter++
				if !checkSseOrCanceled(iter) {
					if sseFailed.Load() {
						return getSseErr()
					}
					return r.Context().Err()
				}
				if sseFailed.Load() {
					return getSseErr()
				}
				if walkErr != nil {
					// d may be nil when error happens early
					dirFlag := false
					if d != nil {
						dirFlag = d.IsDir()
					}
					fail(filepath.ToSlash(p), walkErr.Error(), dirFlag)
					if sseFailed.Load() {
						return getSseErr()
					}
					if dirFlag {
						return filepath.SkipDir
					}
					return nil
				}
				rel, err := filepath.Rel(srcAbs, p)
				if err != nil {
					return err
				}
				if rel == "." {
					if action == "move" {
						// Root directory handled after walk.
					}
					return nil
				}
				srcPath := p
				srcIsDir := d.IsDir()
				if d.Type()&os.ModeSymlink != 0 {
					fail(filepath.ToSlash(srcPath), msg.PathOutsideInstanceRoot, false)
					if sseFailed.Load() {
						return getSseErr()
					}
					return nil
				}
				if excludes.excludes(srcPath, srcIsDir) {
					if srcIsDir {
						return filepath.SkipDir
					}
					return nil
				}
				dstPath := filepath.Join(dstAbs, rel)
				if err := ensurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
					fail(filepath.ToSlash(srcPath), err.Error(), srcIsDir)
					if sseFailed.Load() {
						return getSseErr()
					}
					if srcIsDir {
						return filepath.SkipDir
					}
					return nil
				}
				if srcIsDir {
					if err := ensureDirectoryWithinRoot(rootPath, dstPath); err != nil {
						fail(filepath.ToSlash(srcPath), err.Error(), true)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
					return nil
				}
				st, err := d.Info()
				if err != nil {
					fail(filepath.ToSlash(srcPath), fileBatchMoveFailReason(err), false)
					if sseFailed.Load() {
						return getSseErr()
					}
					return nil
				}
				if action == "copy" && req.CopyDuplicate {
					dstPath, err = resolveCopyFileDestination(dstPath, true)
					if err != nil {
						fail(filepath.ToSlash(srcPath), err.Error(), false)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
					if err := ensurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
						fail(filepath.ToSlash(srcPath), err.Error(), false)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
				}
				if action == "copy" {
					err = copyFileAtomicWithinRoot(rootPath, srcPath, dstPath, st.Mode(), req.Overwrite && !req.CopyDuplicate)
				} else {
					err = moveFileWithinRoot(r.Context(), rootPath, srcPath, dstPath, st.Mode(), req.Overwrite)
				}
				if err != nil {
					if errors.Is(err, os.ErrExist) {
						fail(filepath.ToSlash(srcPath), msg.TargetAlreadyExists, false)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
					fail(filepath.ToSlash(srcPath), fileBatchMoveFailReason(err), false)
					if sseFailed.Load() {
						return getSseErr()
					}
					return nil
				}
				success()
				if sseFailed.Load() {
					return getSseErr()
				}
				return nil
			})
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				if sseFailed.Load() {
					logSseFailureOnce()
					return
				}
				fail(rule.Path, err.Error(), true)
				continue
			}
			if action == "move" {
				if failCount != dirFailCountBeforeMove {
					continue
				}
				if err := removeMovedDirectorySourceWithinRoot(r.Context(), rootPath, srcAbs, len(req.Exclude) > 0); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						return
					}
					fail(rule.Path, deleteFailureReason(err), true)
					continue
				}
			}
			continue
		}

		// File
		if excludes.excludes(srcAbs, false) {
			continue
		}
		if action == "copy" {
			if err := copyFileAtomicWithinRoot(rootPath, srcAbs, dstAbs, info.Mode(), req.Overwrite && !req.CopyDuplicate); err != nil {
				if errors.Is(err, os.ErrExist) {
					fail(rule.Path, msg.TargetAlreadyExists, false)
					continue
				}
				fail(rule.Path, fileBatchMoveFailReason(err), false)
				continue
			}
			success()
			continue
		}
		if action == "move" {
			// Extra guard: when overwrite is disabled, ensure destination does not exist.
			if !req.Overwrite {
				if _, err := os.Stat(dstAbs); err == nil {
					fail(rule.Path, msg.TargetAlreadyExists, false)
					continue
				} else if !errors.Is(err, os.ErrNotExist) {
					fail(rule.Path, err.Error(), false)
					continue
				}
			}
			if err := ensurePathComponentsWithinRoot(rootPath, dstAbs, false); err != nil {
				fail(rule.Path, err.Error(), false)
				continue
			}
			if err := moveFileWithinRoot(r.Context(), rootPath, srcAbs, dstAbs, info.Mode(), req.Overwrite); err != nil {
				if errors.Is(err, os.ErrExist) {
					fail(rule.Path, msg.TargetAlreadyExists, false)
					continue
				}
				fail(rule.Path, err.Error(), false)
				continue
			}
			success()
			continue
		}
	}

	if sseFailed.Load() {
		logSseFailureOnce()
		return
	}
	sendProgress(true)
	reportSseErr(sse.SendEvent("end", map[string]int{"ok": okCount, "fail": failCount}))
	if sseFailed.Load() {
		logSseFailureOnce()
		return
	}
}
