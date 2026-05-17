package api

import (
	"IpacPanel/controller/src/atomic/file"
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"encoding/base64"
	"errors"

	process "IpacPanel/controller/src/process"

	"fmt"
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
	Cursor        string      `json:"cursor,omitempty"`
	NextCursor    string      `json:"next_cursor,omitempty"`
	HasPrev       bool        `json:"has_prev"`
	HasNext       bool        `json:"has_next"`
	RequestedPath string      `json:"requested_path,omitempty"`
	Fallback      bool        `json:"fallback,omitempty"`
	Missing       bool        `json:"missing,omitempty"`
}

const fileListPageSize = 200

type fileListCursor struct {
	Section   string
	LowerName string
	Name      string
}

func parseOptionalBoolQuery(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return false
	}
	return v == "1" || v == "true" || v == "yes" || v == "y" || v == "on"
}

type fileCreateFileRequest struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

type fileCreateDirRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type fileRenameRequest struct {
	Path    string `json:"path"`
	NewName string `json:"new_name"`
}

type fileDeleteRequest struct {
	Path string `json:"path"`
}

type fileContentResponse struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type fileSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fileBatchRule struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type fileBatchActionRequest struct {
	Action        string          `json:"action"`
	DestDir       string          `json:"dest_dir"`
	Overwrite     bool            `json:"overwrite"`
	CopyDuplicate bool            `json:"copy_duplicate"`
	Include       []fileBatchRule `json:"include"`
	Exclude       []fileBatchRule `json:"exclude"`
}

type fileExtractRequest struct {
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

func encodeFileListCursor(item fileEntryLite) string {
	section := "f"
	if item.IsDir {
		section = "d"
	}
	raw := section + "\n" + item.LowerName + "\n" + item.Name
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeFileListCursor(raw string) (*fileListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(decoded), "\n")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, errors.New(msg.InvalidCursor)
	}
	section := strings.TrimSpace(parts[0])
	if section != "d" && section != "f" {
		return nil, errors.New(msg.InvalidCursor)
	}
	name := parts[len(parts)-1]
	if name == "" {
		return nil, errors.New(msg.InvalidCursor)
	}
	lowerName := strings.ToLower(name)
	if len(parts) == 3 {
		lowerName = parts[1]
		if lowerName == "" {
			return nil, errors.New(msg.InvalidCursor)
		}
	}
	return &fileListCursor{Section: section, LowerName: lowerName, Name: name}, nil
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

func buildFileListItems(targetPath string, query string) ([]fileEntryLite, error) {
	if textTooLong(query, maxFileSearchLen) {
		return nil, errors.New(msg.FileQueryTooLong)
	}
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return nil, err
	}

	items := make([]fileEntryLite, 0, len(entries))
	for _, entry := range entries {
		item := fileEntryLite{
			Name:      entry.Name(),
			LowerName: strings.ToLower(entry.Name()),
			IsDir:     entry.IsDir(),
		}
		if query != "" && !strings.Contains(item.LowerName, query) {
			continue
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			item.Size = info.Size()
			item.ModTime = info.ModTime().Format(cfg.FileTimeLayout)
		}
		items = append(items, item)
	}

	sort.Slice(items, func(i int, j int) bool {
		return compareFileListItems(items[i], items[j]) < 0
	})
	return items, nil
}

func scanFileListPage(targetPath string, cursor *fileListCursor, query string, pageSize int) ([]fileEntryLite, string, bool, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	items, err := buildFileListItems(targetPath, query)
	if err != nil {
		return nil, "", false, err
	}

	start := 0
	if cursor != nil {
		cursorItem := fileEntryLite{
			Name:      cursor.Name,
			LowerName: cursor.LowerName,
			IsDir:     cursor.Section == "d",
		}
		start = sort.Search(len(items), func(i int) bool {
			return compareFileListItems(items[i], cursorItem) > 0
		})
	}
	if start >= len(items) {
		return []fileEntryLite{}, "", false, nil
	}

	items = items[start:]
	hasNext := len(items) > pageSize
	if !hasNext {
		return items, "", false, nil
	}
	pageItems := items[:pageSize]
	return pageItems, encodeFileListCursor(pageItems[len(pageItems)-1]), true, nil
}

func buildFileListResponse(sp *process.InstanceProcess, relativePath string, cursorRaw string, query string) (*fileListResponse, error) {
	rootPath, normalizedPath, err := resolveInstanceFilePath(sp, relativePath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, cursorRaw, query, err)
}

func buildFileListJumpResponse(sp *process.InstanceProcess, requestedPath string, cursorRaw string, query string) (*fileListResponse, error) {
	rootPath, normalizedPath, err := resolveFileListJumpPath(sp, requestedPath)
	return buildFileListResponseFromResolvedPath(sp, rootPath, normalizedPath, cursorRaw, query, err)
}

func buildFileListResponseFromResolvedPath(sp *process.InstanceProcess, rootPath string, normalizedPath string, cursorRaw string, query string, resolveErr error) (*fileListResponse, error) {
	if resolveErr != nil {
		return nil, resolveErr
	}

	rootPath = filepath.Clean(rootPath)
	normalizedPath = normalizeRelativeFilePath(normalizedPath)
	targetPath := rootPath
	if normalizedPath != "" {
		targetPath = filepath.Join(rootPath, filepath.FromSlash(normalizedPath))
	}

	return buildFileListResponseFromTargetPath(sp, targetPath, normalizedPath, cursorRaw, query)
}

func buildFileListResponseFromTargetPath(sp *process.InstanceProcess, targetPath string, normalizedPath string, cursorRaw string, query string) (*fileListResponse, error) {
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

	cursor, err := decodeFileListCursor(cursorRaw)
	if err != nil {
		return nil, fmt.Errorf(msg.InvalidCursorFmt, err)
	}

	if err := ensureResolvedPathWithinInstanceRoot(sp, targetPath); err != nil {
		return nil, err
	}
	items, nextCursor, hasNext, err := scanFileListPage(targetPath, cursor, query, fileListPageSize)
	if err != nil {
		return nil, err
	}
	pagedItems := buildPagedFileEntries(items)

	return &fileListResponse{
		Path:       ensureTrailingSlash(normalizedPath),
		Entries:    pagedItems,
		Cursor:     strings.TrimSpace(cursorRaw),
		NextCursor: nextCursor,
		HasPrev:    cursor != nil,
		HasNext:    hasNext,
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
