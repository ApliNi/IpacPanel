//go:build ignore

// Run Build: go run ./build.go
// Run Build for selected platforms: go run ./build.go --platform windows/amd64,linux/arm64
// Run Build with tester: go run ./build.go --build-tester

package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type buildProduct struct {
	Pkg      string
	Artifact string
}

type zipEntry struct {
	SourcePath string
	Name       string
}

type target struct {
	OS   string
	Arch string
}

func (t target) String() string {
	return t.OS + "/" + t.Arch
}

var commonTargets = []target{
	{OS: "windows", Arch: "amd64"},
	{OS: "windows", Arch: "arm64"},
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "freebsd", Arch: "amd64"},
	{OS: "freebsd", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
		os.Exit(1)
	}
}
func run() error {
	platforms := flag.String("platform", "", "comma-separated target platforms, e.g. windows/amd64,linux/arm64")
	buildTester := flag.Bool("build-tester", false, "include tester binary in release archives")
	flag.Parse()

	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	targets, err := selectTargets(*platforms, commonTargets)
	if err != nil {
		return err
	}
	buildDir := filepath.Join(repoRoot, "build")
	if err := recreateDir(buildDir); err != nil {
		return err
	}
	for _, target := range targets {
		if err := buildTarget(repoRoot, buildDir, target, *buildTester); err != nil {
			return err
		}
	}
	fmt.Printf("Build outputs were written to %s\n", buildDir)
	return nil
}

func selectTargets(platforms string, available []target) ([]target, error) {
	requested := parsePlatformList(platforms)
	if len(requested) == 0 {
		return append([]target(nil), available...), nil
	}

	availableByName := make(map[string]target, len(available))
	for _, target := range available {
		availableByName[target.String()] = target
	}

	selected := make([]target, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, platform := range requested {
		if _, ok := seen[platform]; ok {
			continue
		}
		seen[platform] = struct{}{}

		target, ok := availableByName[platform]
		if !ok {
			return nil, fmt.Errorf("unknown platform %q, available platforms: %s", platform, strings.Join(targetNames(available), ","))
		}
		selected = append(selected, target)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no platform selected")
	}
	return selected, nil
}

func parsePlatformList(platforms string) []string {
	parts := strings.Split(platforms, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		platform := strings.ToLower(strings.TrimSpace(part))
		if platform == "" {
			continue
		}
		platform = strings.ReplaceAll(platform, "_", "/")
		platform = strings.ReplaceAll(platform, "-", "/")
		items = append(items, platform)
	}
	return items
}

func targetNames(targets []target) []string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.String())
	}
	return names
}

func recreateDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove build directory %q: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create build directory %q: %w", path, err)
	}
	return nil
}
func buildTarget(repoRoot string, buildDir string, target target, buildTester bool) error {
	products := []buildProduct{
		{Pkg: "./daemon", Artifact: "IpacPanel"},
		{Pkg: "./controller", Artifact: "IpacPanel_Controller"},
	}
	if buildTester {
		products = append(products, buildProduct{Pkg: "./tester", Artifact: "tester"})
	}
	var exePaths []string
	var binaryEntries []zipEntry
	for _, product := range products {
		exePath := filepath.Join(buildDir, artifactName(target, product.Artifact))
		args := []string{"build", "-trimpath", "-ldflags", "-s -w", "-o", exePath, product.Pkg}
		cmd := exec.Command("go", args...)
		cmd.Dir = repoRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+target.OS,
			"GOARCH="+target.Arch,
		)
		fmt.Printf("Building %s/%s %s -> %s\n", target.OS, target.Arch, product.Pkg, exePath)
		if err := cmd.Run(); err != nil {
			return explainBuildError(target, product.Pkg, err)
		}
		exePaths = append(exePaths, exePath)
		cleanName := product.Artifact
		if target.OS == "windows" {
			cleanName += ".exe"
		}
		binaryEntries = append(binaryEntries, zipEntry{SourcePath: exePath, Name: cleanName})
	}
	zipEntries, err := collectReleaseEntries(repoRoot, binaryEntries)
	if err != nil {
		return err
	}

	zipName := fmt.Sprintf("IpacPanel-%s-%s.zip", target.OS, target.Arch)
	zipPath := filepath.Join(buildDir, zipName)
	fmt.Printf("Packaging %s <- %s/%s\n", zipName, target.OS, target.Arch)
	if err := createZip(zipPath, zipEntries); err != nil {
		return fmt.Errorf("create zip %s: %w", zipName, err)
	}

	for _, p := range exePaths {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove temporary artifact %q: %w", p, err)
		}
	}

	return nil
}
func artifactName(target target, name string) string {
	fullName := fmt.Sprintf("%s-%s-%s", name, target.OS, target.Arch)
	if target.OS == "windows" {
		fullName += ".exe"
	}
	return fullName
}
func collectReleaseEntries(repoRoot string, binaryEntries []zipEntry) ([]zipEntry, error) {
	entries := append([]zipEntry(nil), binaryEntries...)

	readme, err := findRequiredRootFile(repoRoot, "README", []string{
		"README.md",
		"README.txt",
		"README.rst",
		"README",
	})
	if err != nil {
		return nil, err
	}
	entries = append(entries, readme)

	license, err := findRequiredRootFile(repoRoot, "LICENSE", []string{
		"LICENSE",
		"LICENSE.md",
		"LICENSE.txt",
		"COPYING",
		"COPYING.md",
		"COPYING.txt",
	})
	if err != nil {
		return nil, err
	}
	entries = append(entries, license)

	userDocs, err := collectDirectoryEntries(repoRoot, filepath.Join("doc", "user_docs"))
	if err != nil {
		return nil, err
	}
	entries = append(entries, userDocs...)

	return entries, nil
}

func findRequiredRootFile(repoRoot string, label string, names []string) (zipEntry, error) {
	checked := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(repoRoot, name)
		checked = append(checked, name)
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return zipEntry{}, fmt.Errorf("required root %s file %q is a directory", label, name)
			}
			return zipEntry{SourcePath: path, Name: filepath.ToSlash(name)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return zipEntry{}, fmt.Errorf("stat root %s candidate %q: %w", label, name, err)
		}
	}

	rootEntries, err := os.ReadDir(repoRoot)
	if err != nil {
		return zipEntry{}, fmt.Errorf("read repository root for %s file: %w", label, err)
	}
	for _, name := range names {
		for _, rootEntry := range rootEntries {
			if !strings.EqualFold(rootEntry.Name(), name) {
				continue
			}
			path := filepath.Join(repoRoot, rootEntry.Name())
			info, err := rootEntry.Info()
			if err != nil {
				return zipEntry{}, fmt.Errorf("stat root %s candidate %q: %w", label, rootEntry.Name(), err)
			}
			if info.IsDir() {
				return zipEntry{}, fmt.Errorf("required root %s file %q is a directory", label, rootEntry.Name())
			}
			return zipEntry{SourcePath: path, Name: filepath.ToSlash(rootEntry.Name())}, nil
		}
	}
	return zipEntry{}, fmt.Errorf("required root %s file not found, checked: %s", label, strings.Join(checked, ", "))
}

func collectDirectoryEntries(repoRoot string, relativeDir string) ([]zipEntry, error) {
	dir := filepath.Join(repoRoot, relativeDir)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("required release directory %q not found", relativeDir)
		}
		return nil, fmt.Errorf("stat release directory %q: %w", relativeDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("required release path %q is not a directory", relativeDir)
	}

	var entries []zipEntry
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk release directory %q: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat release file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release path %q is not a regular file", path)
		}
		relativePath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return fmt.Errorf("resolve release path %q relative to repo root: %w", path, err)
		}
		entries = append(entries, zipEntry{SourcePath: path, Name: filepath.ToSlash(relativePath)})
		return nil
	}); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("required release directory %q contains no files", relativeDir)
	}
	return entries, nil
}

func createZip(zipPath string, entries []zipEntry) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip file %q: %w", zipPath, err)
	}

	w := zip.NewWriter(f)

	for _, entry := range entries {
		if err := addZipEntry(w, entry); err != nil {
			if closeErr := w.Close(); closeErr != nil {
				return fmt.Errorf("%w; additionally close zip writer: %v", err, closeErr)
			}
			if closeErr := f.Close(); closeErr != nil {
				return fmt.Errorf("%w; additionally close zip file: %v", err, closeErr)
			}
			return err
		}
	}
	if err := w.Close(); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return fmt.Errorf("close zip writer: %w; additionally close zip file: %v", err, closeErr)
		}
		return fmt.Errorf("close zip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close zip file %q: %w", zipPath, err)
	}
	return nil
}

func addZipEntry(w *zip.Writer, entry zipEntry) error {
	if entry.Name == "" {
		return fmt.Errorf("zip entry name is empty for %q", entry.SourcePath)
	}
	if strings.Contains(entry.Name, "\\") {
		return fmt.Errorf("zip entry %q uses Windows path separator", entry.Name)
	}

	src, err := os.Open(entry.SourcePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", entry.SourcePath, err)
	}

	info, err := src.Stat()
	if err != nil {
		if closeErr := src.Close(); closeErr != nil {
			return fmt.Errorf("stat %s: %w; additionally close source file: %v", entry.SourcePath, err, closeErr)
		}
		return fmt.Errorf("stat %s: %w", entry.SourcePath, err)
	}
	if !info.Mode().IsRegular() {
		if closeErr := src.Close(); closeErr != nil {
			return fmt.Errorf("zip source %q is not a regular file; additionally close source file: %w", entry.SourcePath, closeErr)
		}
		return fmt.Errorf("zip source %q is not a regular file", entry.SourcePath)
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		if closeErr := src.Close(); closeErr != nil {
			return fmt.Errorf("header %s: %w; additionally close source file: %v", entry.SourcePath, err, closeErr)
		}
		return fmt.Errorf("header %s: %w", entry.SourcePath, err)
	}
	header.Name = entry.Name
	header.Method = zip.Deflate

	writer, err := w.CreateHeader(header)
	if err != nil {
		if closeErr := src.Close(); closeErr != nil {
			return fmt.Errorf("create entry %s: %w; additionally close source file: %v", header.Name, err, closeErr)
		}
		return fmt.Errorf("create entry %s: %w", header.Name, err)
	}

	if _, err := io.Copy(writer, src); err != nil {
		if closeErr := src.Close(); closeErr != nil {
			return fmt.Errorf("write %s: %w; additionally close source file: %v", header.Name, err, closeErr)
		}
		return fmt.Errorf("write %s: %w", header.Name, err)
	}
	if err := src.Close(); err != nil {
		return fmt.Errorf("close %s: %w", entry.SourcePath, err)
	}
	return nil
}

func explainBuildError(target target, pkg string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("build %s/%s %s exited with code %d", target.OS, target.Arch, pkg, exitErr.ExitCode())
	}
	message := err.Error()
	if runtime.GOOS == "windows" && strings.Contains(strings.ToLower(message), "executable file not found") {
		return fmt.Errorf("go command not found in PATH: %w", err)
	}
	return fmt.Errorf("build %s/%s %s: %w", target.OS, target.Arch, pkg, err)
}
