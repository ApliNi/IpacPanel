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
	var zipEntries []string
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
		zipEntries = append(zipEntries, cleanName)
	}

	zipName := fmt.Sprintf("IpacPanel-%s-%s.zip", target.OS, target.Arch)
	zipPath := filepath.Join(buildDir, zipName)
	fmt.Printf("Packaging %s <- %s/%s\n", zipName, target.OS, target.Arch)
	if err := createZip(zipPath, exePaths, zipEntries); err != nil {
		return fmt.Errorf("create zip %s: %w", zipName, err)
	}

	for _, p := range exePaths {
		os.Remove(p)
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
func createZip(zipPath string, files []string, names []string) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	for i, file := range files {
		src, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("open %s: %w", file, err)
		}

		info, err := src.Stat()
		if err != nil {
			src.Close()
			return fmt.Errorf("stat %s: %w", file, err)
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			src.Close()
			return fmt.Errorf("header %s: %w", file, err)
		}
		header.Name = names[i]
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			src.Close()
			return fmt.Errorf("create entry %s: %w", header.Name, err)
		}

		if _, err := io.Copy(writer, src); err != nil {
			src.Close()
			return fmt.Errorf("write %s: %w", header.Name, err)
		}
		src.Close()
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
