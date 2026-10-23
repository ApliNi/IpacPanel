package api

import (
	"IpacPanel/controller/src/instancefs"
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStripArchiveSuffix(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"app.tar.gz", "app"},
		{"app.tar.bz2", "app"},
		{"app.tar.xz", "app"},
		{"app.tar.tgz", "app"},
		{"app.zip", "app"},
		{"app.tar", "app"},
		{"app.gz", "app"},
		{"archive.backup.tar.gz", "archive.backup"},
		{"file.txt", "file"},
		{"noextension", "noextension"},
		{"", ""},
	}

	for _, tc := range cases {
		actual := stripArchiveSuffix(tc.input)
		if actual != tc.expected {
			t.Errorf("stripArchiveSuffix(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}

func createTestZip(t *testing.T, filePath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create zip file: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		writer, err := w.Create(name)
		if err != nil {
			t.Fatalf("failed to add file to zip: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write content to zip: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
}

func createTestTarGz(t *testing.T, filePath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create tar.gz file: %v", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write tar content: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
}

func createTestTar(t *testing.T, filePath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create tar file: %v", err)
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write tar content: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
}

func TestDetectArchiveKind(t *testing.T) {
	tempDir := t.TempDir()

	zipPath := filepath.Join(tempDir, "test.zip")
	createTestZip(t, zipPath, map[string]string{"hello.txt": "world"})

	kind, err := detectArchiveKind(zipPath, "test.zip")
	if err != nil || kind != archiveKindZip {
		t.Fatalf("expected archiveKindZip, got kind=%v, err=%v", kind, err)
	}

	tarGzPath := filepath.Join(tempDir, "test.tar.gz")
	createTestTarGz(t, tarGzPath, map[string]string{"hello.txt": "world"})

	kind, err = detectArchiveKind(tarGzPath, "test.tar.gz")
	if err != nil || kind != archiveKindTarGz {
		t.Fatalf("expected archiveKindTarGz, got kind=%v, err=%v", kind, err)
	}

	tarPath := filepath.Join(tempDir, "test.tar")
	createTestTar(t, tarPath, map[string]string{"hello.txt": "world"})

	kind, err = detectArchiveKind(tarPath, "test.tar")
	if err != nil || kind != archiveKindTar {
		t.Fatalf("expected archiveKindTar, got kind=%v, err=%v", kind, err)
	}

	unsupportedPath := filepath.Join(tempDir, "sample.rar")
	if err := os.WriteFile(unsupportedPath, []byte("Rar!\x1a\x07\x00"), 0644); err != nil {
		t.Fatalf("failed to write dummy rar: %v", err)
	}

	kind, err = detectArchiveKind(unsupportedPath, "sample.rar")
	if !errors.Is(err, errArchiveFormatUnsupported) {
		t.Fatalf("expected errArchiveFormatUnsupported, got err=%v", err)
	}
}

func TestExtractZipArchive(t *testing.T) {
	tempDir := t.TempDir()
	fsRoot := filepath.Join(tempDir, "instance")
	if err := os.MkdirAll(fsRoot, 0755); err != nil {
		t.Fatalf("failed to create instance root: %v", err)
	}

	fs := instancefs.New(fsRoot)
	zipPath := filepath.Join(tempDir, "archive.zip")
	testFiles := map[string]string{
		"folder/file1.txt": "content 1",
		"file2.txt":        "content 2",
	}
	createTestZip(t, zipPath, testFiles)

	targetDir := filepath.Join(fsRoot, "extracted")
	ctx := context.Background()

	result, err := extractArchive(ctx, fs, archiveKindZip, zipPath, targetDir, false, false, nil, nil)
	if err != nil {
		t.Fatalf("extractArchive failed: %v", err)
	}
	if result.skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", result.skipped)
	}

	data1, err := os.ReadFile(filepath.Join(targetDir, "folder", "file1.txt"))
	if err != nil || string(data1) != "content 1" {
		t.Errorf("file1.txt content mismatch: got %q, err %v", string(data1), err)
	}

	data2, err := os.ReadFile(filepath.Join(targetDir, "file2.txt"))
	if err != nil || string(data2) != "content 2" {
		t.Errorf("file2.txt content mismatch: got %q, err %v", string(data2), err)
	}

	// Test skip when overwrite is false.
	resultSkip, err := extractArchive(ctx, fs, archiveKindZip, zipPath, targetDir, false, false, nil, nil)
	if err != nil {
		t.Fatalf("second extractArchive failed: %v", err)
	}
	if resultSkip.skipped != 2 {
		t.Fatalf("expected 2 skipped files, got %d", resultSkip.skipped)
	}
}

func TestExtractTarGzArchive(t *testing.T) {
	tempDir := t.TempDir()
	fsRoot := filepath.Join(tempDir, "instance")
	if err := os.MkdirAll(fsRoot, 0755); err != nil {
		t.Fatalf("failed to create instance root: %v", err)
	}

	fs := instancefs.New(fsRoot)
	tarGzPath := filepath.Join(tempDir, "archive.tar.gz")
	testFiles := map[string]string{
		"nested/sub/data.txt": "tar gzip data",
	}
	createTestTarGz(t, tarGzPath, testFiles)

	targetDir := filepath.Join(fsRoot, "extracted_tar")
	ctx := context.Background()

	result, err := extractArchive(ctx, fs, archiveKindTarGz, tarGzPath, targetDir, false, false, nil, nil)
	if err != nil {
		t.Fatalf("extractArchive failed: %v", err)
	}
	if result.skipped != 0 {
		t.Fatalf("expected 0 skipped, got %d", result.skipped)
	}

	content, err := os.ReadFile(filepath.Join(targetDir, "nested", "sub", "data.txt"))
	if err != nil || string(content) != "tar gzip data" {
		t.Errorf("data.txt content mismatch: got %q, err %v", string(content), err)
	}
}

func TestIdentifyArchiveErrorMessage(t *testing.T) {
	msg1 := identifyArchiveErrorMessage(errArchiveFormatUnsupported)
	if msg1 != "暂不支持该压缩格式" {
		t.Errorf("expected unsupported format message, got %q", msg1)
	}

	msg2 := identifyArchiveErrorMessage(errors.New("io error"))
	if msg2 != "压缩包分析失败, 请确认文件格式正确且文件未损坏" {
		t.Errorf("expected analyze archive failed message, got %q", msg2)
	}
}

func TestCalcExtractProgressPercent(t *testing.T) {
	if p := calcExtractProgressPercent(5, 10); p != 50.0 {
		t.Errorf("expected 50.0, got %f", p)
	}
	if p := calcExtractProgressPercent(0, 0); p != 0.0 {
		t.Errorf("expected 0.0, got %f", p)
	}
	if p := calcExtractProgressPercentByBytes(0); p != 0.0 {
		t.Errorf("expected 0.0, got %f", p)
	}
	if p := calcExtractProgressPercentByBytes(1024 * 1024 * 8); p <= 0 || p >= 95 {
		t.Errorf("expected between 0 and 95, got %f", p)
	}
}
