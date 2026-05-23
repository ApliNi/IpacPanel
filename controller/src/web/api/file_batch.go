package api

import (
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/web/authz"

	web "IpacPanel/controller/src/web"

	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type fileBatchExcludeMatcher struct {
	dirs  []string
	files map[string]struct{}
}

func cleanAndEnsureDirSlash(p string) string {
	pp := strings.TrimSpace(p)
	if pp == "" {
		return ""
	}
	pp = filepath.Clean(pp)
	sep := string(filepath.Separator)
	if !strings.HasSuffix(pp, sep) {
		pp += sep
	}
	return pp
}

func isSameOrSubDir(parentDir string, candidateDir string) bool {
	parent := cleanAndEnsureDirSlash(parentDir)
	cand := cleanAndEnsureDirSlash(candidateDir)
	if parent == "" || cand == "" {
		return false
	}
	return strings.HasPrefix(cand, parent)
}

func applyExcludeRules(path string, isDir bool, exclude []fileBatchRule) bool {
	matcher := newFileBatchExcludeMatcher(exclude)
	return matcher.excludes(path, isDir)
}

func newFileBatchExcludeMatcher(exclude []fileBatchRule) fileBatchExcludeMatcher {
	matcher := fileBatchExcludeMatcher{files: make(map[string]struct{})}
	for _, r := range exclude {
		rp := strings.TrimSpace(r.Path)
		if rp == "" {
			continue
		}
		rp = filepath.Clean(rp)
		if r.IsDir {
			matcher.dirs = append(matcher.dirs, cleanAndEnsureDirSlash(rp))
			continue
		}
		matcher.files[filepath.Clean(rp)] = struct{}{}
	}
	return matcher
}

func (m fileBatchExcludeMatcher) excludes(path string, isDir bool) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	if isDir {
		clean = cleanAndEnsureDirSlash(clean)
	}
	for _, rp := range m.dirs {
		if strings.HasPrefix(clean, rp) {
			return true
		}
	}
	_, ok := m.files[filepath.Clean(clean)]
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
		web.WriteAPIError(w, http.StatusBadRequest, msg.FilePathInvalid, errors.New(msg.EmptyDest))
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

	excludes := newFileBatchExcludeMatcher(req.Exclude)

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
		info, err := os.Stat(srcAbs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fail(rule.Path, msg.TargetNotFound, rule.IsDir)
			} else {
				fail(rule.Path, err.Error(), rule.IsDir)
			}
			continue
		}

		isDir := info.IsDir()
		baseName := filepath.Base(srcAbs)
		dstAbs := destAbs
		if action == "copy" || action == "move" {
			dstAbs = filepath.Join(destAbs, baseName)
			if action == "copy" && req.CopyDuplicate && isDir {
				dstAbs, err = resolveCopyDirectoryDestination(dstAbs, true)
				if err != nil {
					fail(rule.Path, err.Error(), true)
					continue
				}
			}
			if err := ensurePathComponentsWithinRoot(rootPath, dstAbs, false); err != nil {
				fail(rule.Path, msg.FilePathInvalid, isDir)
				continue
			}
		}

		// Apply subdir protection for directory rules in copy/move after final destination is known.
		if (action == "copy" || action == "move") && isDir {
			if isSameOrSubDir(srcAbs, dstAbs) {
				fail(rule.Path, msg.TargetDirectoryInsideSource, true)
				continue
			}
		}

		// Prevent copying/moving onto itself (especially dangerous for directories).
		if action == "copy" || action == "move" {
			if filepath.Clean(srcAbs) == filepath.Clean(dstAbs) {
				fail(rule.Path, msg.TargetSameAsSource, isDir)
				continue
			}
		}

		if action == "delete" {
			if excludes.excludes(srcAbs, isDir) {
				continue
			}
			if isDir {
				if err := removeAllWithinRoot(rootPath, srcAbs); err != nil {
					fail(rule.Path, err.Error(), true)
					continue
				}
				success()
				continue
			}
			if err := removeFileWithinRoot(rootPath, srcAbs); err != nil {
				fail(rule.Path, err.Error(), false)
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
					fail(filepath.ToSlash(srcPath), err.Error(), false)
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
				if err := copyFileAtomicWithinRoot(rootPath, srcPath, dstPath, st.Mode(), req.Overwrite && !req.CopyDuplicate); err != nil {
					if errors.Is(err, os.ErrExist) {
						fail(filepath.ToSlash(srcPath), msg.TargetAlreadyExists, false)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
					fail(filepath.ToSlash(srcPath), err.Error(), false)
					if sseFailed.Load() {
						return getSseErr()
					}
					return nil
				}
				if action == "move" {
					if err := removeFileWithinRoot(rootPath, srcPath); err != nil {
						fail(filepath.ToSlash(srcPath), err.Error(), false)
						if sseFailed.Load() {
							return getSseErr()
						}
						return nil
					}
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
				// Remove the root directory if empty.
				_ = removeEmptyDirectoryWithinRoot(rootPath, srcAbs)
			}
			continue
		}

		// File
		if excludes.excludes(srcAbs, false) {
			continue
		}
		if action == "copy" {
			if req.CopyDuplicate {
				dstAbs, err = resolveCopyFileDestination(dstAbs, true)
				if err != nil {
					fail(rule.Path, err.Error(), false)
					continue
				}
			}
			if err := ensurePathComponentsWithinRoot(rootPath, dstAbs, false); err != nil {
				fail(rule.Path, err.Error(), false)
				continue
			}
			if err := copyFileAtomicWithinRoot(rootPath, srcAbs, dstAbs, info.Mode(), req.Overwrite && !req.CopyDuplicate); err != nil {
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
			if err := renameOrCopyFileWithinRoot(rootPath, srcAbs, dstAbs, info.Mode(), !req.Overwrite); err != nil {
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
