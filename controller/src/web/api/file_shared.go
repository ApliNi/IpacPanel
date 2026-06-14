package api

import (
	"IpacPanel/controller/src/atomic/file"
	"IpacPanel/controller/src/compat"
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/instancefs"
	"IpacPanel/controller/src/msg"
	"container/heap"
	"context"
	"errors"

	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	process "IpacPanel/controller/src/process"
)

const (
	maxFileSearchLen   = 4096
	maxFilePathTextLen = 4096
)

func textTooLong(value string, maxLen int) bool {
	return utf8.RuneCountInString(value) > maxLen
}

func ensurePathComponentsWithinRoot(rootPath string, targetPath string, includeLeaf bool) error {
	return instancefs.EnsurePathComponentsWithinRoot(rootPath, targetPath, includeLeaf)
}

func ensureResolvedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	return instancefs.EnsureResolvedPathWithinInstanceRoot(sp, targetPath)
}

func ensureNewPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	return instancefs.EnsureNewPathWithinInstanceRoot(sp, targetPath)
}

func ensureCreatedPathWithinInstanceRoot(sp *process.InstanceProcess, targetPath string) error {
	return ensureResolvedPathWithinInstanceRoot(sp, targetPath)
}

func resolveInstanceFilePath(sp *process.InstanceProcess, relativePath string) (string, string, error) {
	return instancefs.ResolveInstanceFilePath(sp, relativePath)
}

func ensureDirectoryWithinRoot(rootPath string, dirPath string) error {
	return instancefs.EnsureDirectoryWithinRoot(rootPath, dirPath)
}

func copyFileAtomicWithinRoot(rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	return instancefs.CopyFileAtomicWithinRoot(rootPath, srcPath, dstPath, mode, overwrite)
}

func copyFileAtomicWithinRootContext(ctx context.Context, rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	return instancefs.CopyFileAtomicWithinRootContext(ctx, rootPath, srcPath, dstPath, mode, overwrite)
}

func writeFileAtomicWithinRoot(rootPath string, targetPath string, data []byte, overwrite bool, mode os.FileMode) error {
	return instancefs.WriteFileAtomicWithinRoot(rootPath, targetPath, data, overwrite, mode)
}

func writeNewFileAtomic(sp *process.InstanceProcess, targetPath string, data []byte, overwrite bool, mode os.FileMode) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return errors.New(msg.EmptyDest)
	}
	rootPath, err := instancefs.GetInstanceRootPath(sp)
	if err != nil {
		return err
	}
	if err := instancefs.EnsureNewPathWithinInstanceRoot(sp, targetPath); err != nil {
		return err
	}
	return writeFileAtomicWithinRoot(rootPath, targetPath, data, overwrite, mode)
}

func writeFileAtomic(rootPath string, path string, data []byte, mode os.FileMode) error {
	return writeFileAtomicWithinRoot(rootPath, path, data, true, mode)
}

func removeFileWithinRoot(rootPath string, targetPath string) error {
	return instancefs.RemoveFileWithinRoot(rootPath, targetPath)
}

func removeEmptyDirectoryWithinRoot(rootPath string, targetPath string) error {
	return instancefs.RemoveEmptyDirectoryWithinRoot(rootPath, targetPath)
}

func removeAllWithinRoot(rootPath string, targetPath string) error {
	return instancefs.RemoveAllWithinRoot(rootPath, targetPath)
}

func newInstanceFS(sp *process.InstanceProcess) (*instancefs.InstanceFS, error) {
	return instancefs.NewFromProcess(sp)
}

func ensureFileName(name string) (string, error) {
	return instancefs.EnsureFileName(name)
}

func renameFileOnlyWithinRoot(rootPath string, srcPath string, dstPath string, overwrite bool) error {
	return instancefs.RenameFileOnlyWithinRoot(rootPath, srcPath, dstPath, overwrite)
}

type moveFileCopiedRemoveSourceError struct {
	SrcPath string
	DstPath string
	Err     error
}

func (err *moveFileCopiedRemoveSourceError) Error() string {
	return "文件已复制, 删除源失败: " + err.Err.Error()
}

func (err *moveFileCopiedRemoveSourceError) Unwrap() error {
	return err.Err
}

func moveFileWithinRoot(ctx context.Context, rootPath string, srcPath string, dstPath string, mode os.FileMode, overwrite bool) error {
	if err := renameFileOnlyWithinRoot(rootPath, srcPath, dstPath, overwrite); err != nil {
		if !compat.IsCrossDeviceRenameError(err) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyFileAtomicWithinRootContext(ctx, rootPath, srcPath, dstPath, mode, overwrite); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := removeFileWithinRoot(rootPath, srcPath); err != nil {
			return &moveFileCopiedRemoveSourceError{SrcPath: srcPath, DstPath: dstPath, Err: err}
		}
	}
	return nil
}

func ensureFileMoveDestinationWithinRoot(rootPath string, dstPath string, noReplace bool) error {
	if err := instancefs.EnsurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
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
		return instancefs.ErrUploadTargetIsDirectory
	}
	return nil
}

func renameDirectoryWithinRoot(rootPath string, srcPath string, dstPath string) error {
	if err := instancefs.EnsurePathComponentsWithinRoot(rootPath, dstPath, false); err != nil {
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
	return instancefs.EnsurePathComponentsWithinRoot(rootPath, dstPath, true)
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
		if _, err := instancefs.EnsureFileName(part); err != nil {
			return "", err
		}
		normalizedParts = append(normalizedParts, part)
	}
	if len(normalizedParts) == 0 {
		return "", errors.New(msg.FileNameRequired)
	}
	return strings.Join(normalizedParts, "/"), nil
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
	rootPath, normalizedPath, err := instancefs.ResolveInstanceFilePath(sp, relativePath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, page, query, err)
}

func buildFileListJumpResponse(sp *process.InstanceProcess, requestedPath string, page int, query string) (*fileListResponse, error) {
	rootPath, normalizedPath, err := instancefs.ResolveFileListJumpPath(sp, requestedPath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, page, query, err)
}

func buildFileListResponseFromResolvedPath(sp *process.InstanceProcess, rootPath string, normalizedPath string, page int, query string, resolveErr error) (*fileListResponse, error) {
	if resolveErr != nil {
		return nil, resolveErr
	}

	rootPath = filepath.Clean(rootPath)
	normalizedPath = instancefs.NormalizeRelativeFilePath(normalizedPath)
	targetPath := rootPath
	if normalizedPath != "" {
		targetPath = filepath.Join(rootPath, filepath.FromSlash(normalizedPath))
	}

	return buildFileListResponseFromTargetPath(sp, targetPath, normalizedPath, page, query)
}

func buildFileListResponseFromTargetPath(sp *process.InstanceProcess, targetPath string, normalizedPath string, page int, query string) (*fileListResponse, error) {
	targetPath = filepath.Clean(targetPath)
	normalizedPath = instancefs.NormalizeRelativeFilePath(normalizedPath)

	if err := instancefs.EnsureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		return nil, err
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New(msg.PathNotDirectory)
	}

	if err := instancefs.EnsureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
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
