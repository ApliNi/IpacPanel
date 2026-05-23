package api

import (
	"IpacPanel/controller/src/atomic/file"
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"container/heap"
	"errors"

	process "IpacPanel/controller/src/process"

	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxFileNameLen     = 255
	maxFileSearchLen   = 4096
	maxFilePathTextLen = 4096
)

func textTooLong(value string, maxLen int) bool {
	return utf8.RuneCountInString(value) > maxLen
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

func ensureResolvedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	rootAbs, err := getInstanceRootPath(sp)
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
	if !isPathWithinRoot(rootReal, targetReal) {
		return errors.New(msg.PathOutsideInstanceRoot)
	}
	return nil
}

func ensureNewPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	rootAbs, err := getInstanceRootPath(sp)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf(msg.InstanceRootPathInvalidFmt, err)
	}

	// targetPath may not exist yet (e.g. rename destination). Validate its parent.
	parent := filepath.Dir(targetPath)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf(msg.PathInvalidFmt, err)
	}
	if !isPathWithinRoot(rootReal, parentReal) {
		return errors.New(msg.PathOutsideInstanceRoot)
	}
	return nil
}

func ensureCreatedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	return ensureResolvedPathWithinInstanceRoot(sp, targetPath)
}

func ensurePathComponentsWithinRoot(rootPath string, targetPath string, includeLeaf bool) error {
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
		return errors.New(msg.PathOutsideInstanceRoot)
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
		part := strings.TrimSpace(parts[i])
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
			return errors.New(msg.PathOutsideInstanceRoot)
		}
		realCurrent, realErr := filepath.EvalSymlinks(current)
		if realErr != nil {
			return fmt.Errorf(msg.PathInvalidFmt, realErr)
		}
		if !isPathWithinRoot(cleanRoot, realCurrent) {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
	}
	return nil
}

func openAtomicTempFileWithinRoot(rootPath string, targetPath string, mode os.FileMode) (*os.File, string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return nil, "", errors.New(msg.EmptyDest)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return nil, "", err
	}
	dir := filepath.Dir(targetPath)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return nil, "", err
	}
	tmp, tmpPath, err := file.OpenTempForTarget(targetPath, mode)
	if err != nil {
		return nil, "", err
	}
	if err := ensurePathComponentsWithinRoot(rootPath, tmpPath, true); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return nil, "", err
	}
	return tmp, tmpPath, nil
}

func commitAtomicTempFileWithinRoot(rootPath string, tempPath string, targetPath string, overwrite bool) error {
	tempPath = strings.TrimSpace(tempPath)
	targetPath = strings.TrimSpace(targetPath)
	if tempPath == "" || targetPath == "" {
		return errors.New(msg.EmptyDest)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, tempPath, true); err != nil {
		return err
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
		if info.IsDir() {
			return errors.New(msg.UploadTargetIsDirectory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := file.CommitTemp(tempPath, targetPath, overwrite, true); err != nil {
		return err
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		_ = os.Remove(targetPath)
		return err
	}
	return nil
}

func commitAtomicTempDirWithinRoot(rootPath string, tempDir string, targetPath string, overwrite bool) error {
	tempDir = strings.TrimSpace(tempDir)
	targetPath = strings.TrimSpace(targetPath)
	if tempDir == "" || targetPath == "" {
		return errors.New(msg.EmptyDest)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, false); err != nil {
		return err
	}
	if info, err := os.Lstat(targetPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := file.CommitTempDir(tempDir, targetPath, file.DirOptions{Overwrite: overwrite, SyncDir: true}); err != nil {
		return err
	}
	return ensurePathComponentsWithinRoot(rootPath, targetPath, true)
}

func ensureDirectoryWithinRoot(rootPath string, dirPath string) error {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return errors.New(msg.EmptyDest)
	}
	if err := ensurePathComponentsWithinRoot(rootPath, dirPath, false); err != nil {
		return err
	}
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return err
	}
	return ensurePathComponentsWithinRoot(rootPath, dirPath, true)
}

func copyFileAtomicWithinRoot(rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	tmp, tmpPath, err := openAtomicTempFileWithinRoot(rootPath, dstPath, mode)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := commitAtomicTempFileWithinRoot(rootPath, tmpPath, dstPath, overwrite); err != nil {
		return err
	}
	committed = true
	return nil
}

func ensureFileMoveDestinationWithinRoot(rootPath string, dstPath string, noReplace bool) error {
	if err := ensurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
		return err
	}
	info, err := os.Lstat(dstPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if noReplace {
		return os.ErrExist
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New(msg.PathOutsideInstanceRoot)
	}
	if info.IsDir() {
		return errors.New(msg.UploadTargetIsDirectory)
	}
	return nil
}

func renameDirectoryWithinRoot(rootPath string, srcPath string, dstPath string) error {
	if err := ensurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
		return err
	}
	if info, err := os.Lstat(dstPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(srcPath, dstPath); err != nil {
		return err
	}
	if err := file.SyncDir(filepath.Dir(dstPath)); err != nil {
		return err
	}
	return ensurePathComponentsWithinRoot(rootPath, dstPath, true)
}

func removeFileWithinRoot(rootPath string, targetPath string) error {
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New(msg.UploadTargetIsDirectory)
	}
	return os.Remove(targetPath)
}

func removeEmptyDirectoryWithinRoot(rootPath string, targetPath string) error {
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New(msg.DestinationNotDirectory)
	}
	return os.Remove(targetPath)
}

func removeAllWithinRoot(rootPath string, targetPath string) error {
	if err := ensurePathComponentsWithinRoot(rootPath, targetPath, true); err != nil {
		return err
	}
	if err := filepath.WalkDir(targetPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ensurePathComponentsWithinRoot(rootPath, path, true); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New(msg.PathOutsideInstanceRoot)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(targetPath)
}

func ensureUploadRelativePath(path string) (string, error) {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" {
		return "", errors.New(msg.FileNameRequired)
	}
	if textTooLong(path, maxFilePathTextLen) {
		return "", errors.New(msg.PathTooLong)
	}
	if strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") {
		return "", errors.New(msg.PathOutsideInstanceRoot)
	}
	osPath := strings.ReplaceAll(path, "/", string(filepath.Separator))
	if filepath.IsAbs(osPath) || filepath.VolumeName(osPath) != "" {
		return "", errors.New(msg.PathOutsideInstanceRoot)
	}
	parts := strings.Split(path, "/")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return "", errors.New(msg.FileNameInvalid)
		}
		if _, err := ensureFileName(part); err != nil {
			return "", err
		}
		normalizedParts = append(normalizedParts, part)
	}
	if len(normalizedParts) == 0 {
		return "", errors.New(msg.FileNameRequired)
	}
	return strings.Join(normalizedParts, "/"), nil
}

func writeFileAtomicWithinRoot(rootPath string, targetPath string, data []byte, overwrite bool, mode os.FileMode) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return errors.New(msg.EmptyDest)
	}
	tmp, tmpPath, err := openAtomicTempFileWithinRoot(rootPath, targetPath, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if len(data) > 0 {
		if _, err := tmp.Write(data); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return commitAtomicTempFileWithinRoot(rootPath, tmpPath, targetPath, overwrite)
}

func writeNewFileAtomic(sp *process.InstanceProcess, targetPath string, data []byte, overwrite bool, mode os.FileMode) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return errors.New(msg.EmptyDest)
	}
	rootPath, err := getInstanceRootPath(sp)
	if err != nil {
		return err
	}
	if err := ensureNewPathWithinInstanceRoot(sp, targetPath); err != nil {
		return err
	}
	return writeFileAtomicWithinRoot(rootPath, targetPath, data, overwrite, mode)
}

func renameOrCopyFile(srcPath string, dstPath string, mode os.FileMode, noReplace bool) error {
	if noReplace {
		if err := file.CommitTemp(srcPath, dstPath, false, true); err == nil {
			return nil
		}
	} else {
		if err := os.Rename(srcPath, dstPath); err == nil {
			return file.SyncDir(filepath.Dir(dstPath))
		}
	}
	if err := file.CopyFile(srcPath, dstPath, file.Options{Overwrite: !noReplace, Mode: mode, SyncDir: true}); err != nil {
		return err
	}
	return os.Remove(srcPath)
}

func renameOrCopyFileWithinRoot(rootPath string, srcPath string, dstPath string, mode os.FileMode, noReplace bool) error {
	if err := ensureFileMoveDestinationWithinRoot(rootPath, dstPath, noReplace); err != nil {
		return err
	}
	if err := copyFileAtomicWithinRoot(rootPath, srcPath, dstPath, mode, !noReplace); err != nil {
		return err
	}
	return removeFileWithinRoot(rootPath, srcPath)
}

func writeFileAtomic(rootPath string, path string, data []byte, mode os.FileMode) error {
	return writeFileAtomicWithinRoot(rootPath, path, data, true, mode)
}

type fileEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type fileEntryLite struct {
	Name      string
	LowerName string
	IsDir     bool
	Size      int64
	ModTime   string
}

type fileListResponse struct {
	Path          string      `json:"path"`
	Entries       []fileEntry `json:"entries"`
	Page          int         `json:"page"`
	PageSize      int         `json:"page_size"`
	TotalCount    int         `json:"total_count"`
	TotalPages    int         `json:"total_pages"`
	HasPrev       bool        `json:"has_prev"`
	HasNext       bool        `json:"has_next"`
	RequestedPath string      `json:"requested_path,omitempty"`
	Fallback      bool        `json:"fallback,omitempty"`
	Missing       bool        `json:"missing,omitempty"`
}

const (
	fileListPageSize      = 200
	fileListScanBatchSize = 256
	maxFileListHeapItems  = 10000
)

type fileCreateFileRequest struct {
	Instance  string `json:"instance"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

type fileCreateDirRequest struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Name     string `json:"name"`
}

type fileRenameRequest struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	NewName  string `json:"new_name"`
}

type fileDeleteRequest struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
}

type fileContentResponse struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type fileSaveRequest struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Content  string `json:"content"`
}

type fileBatchRule struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type fileBatchActionRequest struct {
	Instance      string          `json:"instance"`
	Action        string          `json:"action"`
	DestDir       string          `json:"dest_dir"`
	Overwrite     bool            `json:"overwrite"`
	CopyDuplicate bool            `json:"copy_duplicate"`
	Include       []fileBatchRule `json:"include"`
	Exclude       []fileBatchRule `json:"exclude"`
}

type fileExtractRequest struct {
	Instance    string `json:"instance"`
	Path        string `json:"path"`
	TargetPath  string `json:"target_path"`
	ExtractHere bool   `json:"extract_here"`
	Overwrite   bool   `json:"overwrite"`
}

func getInstanceRootPath(sp *process.InstanceProcess) (string, error) {
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

func normalizeRelativeFilePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.TrimPrefix(p, "/")
	p = filepath.Clean(strings.ReplaceAll(p, "/", string(filepath.Separator)))
	if p == "." {
		return ""
	}
	return p
}

func resolveInstanceFilePath(sp *process.InstanceProcess, relativePath string) (string, string, error) {
	rootPath, err := getInstanceRootPath(sp)
	if err != nil {
		return "", "", err
	}

	relativePath = strings.TrimSpace(relativePath)
	if textTooLong(relativePath, maxFilePathTextLen) {
		return "", "", errors.New(msg.PathTooLong)
	}
	if relativePath != "" {
		osPath := strings.ReplaceAll(relativePath, "\\", string(filepath.Separator))
		osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
		if filepath.IsAbs(osPath) || isWindowsAbsolutePath(relativePath) || strings.HasPrefix(relativePath, "/") || strings.HasPrefix(relativePath, "\\") {
			return "", "", errors.New(msg.PathOutsideInstanceRoot)
		}
	}

	relativePath = normalizeRelativeFilePath(relativePath)
	targetPath := rootPath
	if relativePath != "" {
		targetPath = filepath.Join(rootPath, relativePath)
	}
	targetPath = filepath.Clean(targetPath)

	relToRoot, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return "", "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", "", errors.New(msg.PathOutsideInstanceRoot)
	}

	normalizedRelative := ""
	if relToRoot != "." {
		normalizedRelative = filepath.ToSlash(relToRoot)
	}

	return rootPath, normalizedRelative, nil
}

func resolveFileListJumpPath(sp *process.InstanceProcess, requestedPath string) (string, string, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if textTooLong(requestedPath, maxFilePathTextLen) {
		return "", "", errors.New(msg.PathTooLong)
	}
	if requestedPath == "" {
		return resolveInstanceFilePath(sp, "")
	}

	osPath := strings.ReplaceAll(requestedPath, "\\", string(filepath.Separator))
	osPath = strings.ReplaceAll(osPath, "/", string(filepath.Separator))
	windowsAbsolute := isWindowsAbsolutePath(requestedPath)
	if windowsAbsolute && !filepath.IsAbs(osPath) {
		return "", "", errors.New(msg.PathOutsideInstanceRoot)
	}
	if !filepath.IsAbs(osPath) {
		return resolveInstanceFilePath(sp, requestedPath)
	}

	rootPath, err := getInstanceRootPath(sp)
	if err != nil {
		return "", "", err
	}
	targetPath := filepath.Clean(osPath)
	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
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

func isWindowsAbsolutePath(p string) bool {
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

func ensureTrailingSlash(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func buildPagedFileEntries(items []fileEntryLite) []fileEntry {
	entries := make([]fileEntry, 0, len(items))
	for _, item := range items {
		entry := fileEntry{
			Name:    item.Name,
			IsDir:   item.IsDir,
			Size:    item.Size,
			ModTime: item.ModTime,
		}
		entries = append(entries, entry)
	}
	return entries
}

func compareFileListItems(a fileEntryLite, b fileEntryLite) int {
	if a.IsDir != b.IsDir {
		if a.IsDir {
			return -1
		}
		return 1
	}
	if a.LowerName != b.LowerName {
		if a.LowerName < b.LowerName {
			return -1
		}
		return 1
	}
	if a.Name != b.Name {
		if a.Name < b.Name {
			return -1
		}
		return 1
	}
	return 0
}

type fileListMaxHeap []fileEntryLite

func (h fileListMaxHeap) Len() int { return len(h) }

func (h fileListMaxHeap) Less(i int, j int) bool {
	return compareFileListItems(h[i], h[j]) > 0
}

func (h fileListMaxHeap) Swap(i int, j int) { h[i], h[j] = h[j], h[i] }

func (h *fileListMaxHeap) Push(x any) {
	*h = append(*h, x.(fileEntryLite))
}

func (h *fileListMaxHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func countFileListEntries(targetPath string, query string) (int, error) {
	dir, err := os.Open(targetPath)
	if err != nil {
		return 0, err
	}
	defer dir.Close()

	totalCount := 0
	for {
		entries, readErr := dir.ReadDir(fileListScanBatchSize)
		for _, entry := range entries {
			name := entry.Name()
			if query != "" && !strings.Contains(strings.ToLower(name), query) {
				continue
			}
			totalCount++
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return 0, readErr
	}
	return totalCount, nil
}

func scanFileListPageItems(targetPath string, page int, query string, pageSize int) ([]fileEntryLite, error) {
	topK := page * pageSize

	dir, err := os.Open(targetPath)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	items := &fileListMaxHeap{}
	heap.Init(items)
	for {
		entries, readErr := dir.ReadDir(fileListScanBatchSize)
		for _, entry := range entries {
			name := entry.Name()
			item := fileEntryLite{
				Name:      name,
				LowerName: strings.ToLower(name),
				IsDir:     entry.IsDir(),
			}
			if query != "" && !strings.Contains(item.LowerName, query) {
				continue
			}
			if items.Len() < topK {
				heap.Push(items, item)
				continue
			}
			if compareFileListItems(item, (*items)[0]) < 0 {
				(*items)[0] = item
				heap.Fix(items, 0)
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return nil, readErr
	}

	selected := make([]fileEntryLite, items.Len())
	copy(selected, *items)
	sort.Slice(selected, func(i int, j int) bool {
		return compareFileListItems(selected[i], selected[j]) < 0
	})

	start := (page - 1) * pageSize
	if start >= len(selected) {
		return []fileEntryLite{}, nil
	}
	end := start + pageSize
	if end > len(selected) {
		end = len(selected)
	}
	pageItems := selected[start:end]
	filteredItems := pageItems[:0]
	for _, item := range pageItems {
		info, infoErr := os.Lstat(filepath.Join(targetPath, item.Name))
		if infoErr != nil {
			if errors.Is(infoErr, os.ErrNotExist) {
				continue
			}
			return nil, infoErr
		}
		item.Size = info.Size()
		item.ModTime = info.ModTime().Format(cfg.FileTimeLayout)
		filteredItems = append(filteredItems, item)
	}

	return filteredItems, nil
}

func scanFileListPage(targetPath string, page int, query string, pageSize int) ([]fileEntryLite, int, int, int, error) {
	if page < 0 {
		return nil, 0, 0, 0, errors.New(msg.InvalidPage)
	}
	if page == 0 {
		page = 1
	}
	if textTooLong(query, maxFileSearchLen) {
		return nil, 0, 0, 0, errors.New(msg.FileQueryTooLong)
	}
	query = strings.ToLower(strings.TrimSpace(query))

	totalCount, err := countFileListEntries(targetPath, query)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	totalPages := (totalCount + pageSize - 1) / pageSize
	if totalPages == 0 {
		return []fileEntryLite{}, totalCount, totalPages, 1, nil
	}
	if page > totalPages {
		page = totalPages
	}
	if page > maxFileListHeapItems/pageSize {
		return nil, 0, 0, 0, errors.New(msg.FileListPageTooDeep)
	}

	pageItems, err := scanFileListPageItems(targetPath, page, query, pageSize)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	return pageItems, totalCount, totalPages, page, nil
}

func buildFileListResponse(sp *process.InstanceProcess, relativePath string, page int, query string) (*fileListResponse, error) {
	rootPath, normalizedPath, err := resolveInstanceFilePath(sp, relativePath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, page, query, err)
}

func buildFileListJumpResponse(sp *process.InstanceProcess, requestedPath string, page int, query string) (*fileListResponse, error) {
	rootPath, normalizedPath, err := resolveFileListJumpPath(sp, requestedPath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, page, query, err)
}

func buildFileListResponseFromResolvedPath(sp *process.InstanceProcess, rootPath string, normalizedPath string, page int, query string, resolveErr error) (*fileListResponse, error) {
	if resolveErr != nil {
		return nil, resolveErr
	}

	rootPath = filepath.Clean(rootPath)
	normalizedPath = normalizeRelativeFilePath(normalizedPath)
	targetPath := rootPath
	if normalizedPath != "" {
		targetPath = filepath.Join(rootPath, filepath.FromSlash(normalizedPath))
	}

	return buildFileListResponseFromTargetPath(sp, targetPath, normalizedPath, page, query)
}

func buildFileListResponseFromTargetPath(sp *process.InstanceProcess, targetPath string, normalizedPath string, page int, query string) (*fileListResponse, error) {
	targetPath = filepath.Clean(targetPath)
	normalizedPath = normalizeRelativeFilePath(normalizedPath)

	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		return nil, err
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New(msg.PathNotDirectory)
	}

	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		return nil, err
	}
	items, totalCount, totalPages, page, err := scanFileListPage(targetPath, page, query, fileListPageSize)
	if err != nil {
		return nil, err
	}
	pagedItems := buildPagedFileEntries(items)

	return &fileListResponse{
		Path:       ensureTrailingSlash(normalizedPath),
		Entries:    pagedItems,
		Page:       page,
		PageSize:   fileListPageSize,
		TotalCount: totalCount,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    totalPages > 0 && page < totalPages,
	}, nil
}

func ensureFileName(name string) (string, error) {
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
