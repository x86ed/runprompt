// tools/build.go - Pure Go cross-compilation build tool
//
// This tool handles:
// - Version bumping logic (parse git tags, increment major/minor/patch)
// - Cross-compilation for multiple platforms
// - Binary naming with appropriate extensions
// - SHA256 checksum generation
//
// Usage:
//   go run tools/build.go --version-bump=patch
//   go run tools/build.go --version-bump=minor
//   go run tools/build.go --version-bump=major
//   go run tools/build.go --version=v1.2.3  # Use explicit version

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Target defines a build target with OS and architecture
type Target struct {
	OS   string
	Arch string
}

// All supported build targets
var targets = []Target{
	// Linux
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "linux", Arch: "386"},
	// Darwin (macOS)
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	// Windows
	{OS: "windows", Arch: "amd64"},
	{OS: "windows", Arch: "arm64"},
	{OS: "windows", Arch: "386"},
}

const (
	defaultVersion = "v0.1.0"
	binaryName     = "runprompt"
	distDir        = "dist"
)

func main() {
	versionBump := flag.String("version-bump", "", "Version bump type: major, minor, or patch")
	explicitVersion := flag.String("version", "", "Explicit version to use (e.g., v1.2.3)")
	flag.Parse()

	// Determine version
	var version string
	var err error

	if *explicitVersion != "" {
		version = *explicitVersion
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !isValidSemver(version) {
			fmt.Fprintf(os.Stderr, "Error: Invalid version format: %s\n", version)
			os.Exit(1)
		}
	} else if *versionBump != "" {
		version, err = bumpVersion(*versionBump)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Error: Either --version-bump or --version is required")
		flag.Usage()
		os.Exit(1)
	}

	fmt.Printf("Building version: %s\n", version)

	// Create dist directory
	if err := os.MkdirAll(distDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating dist directory: %v\n", err)
		os.Exit(1)
	}

	// Build for all targets
	var builtFiles []string
	for _, target := range targets {
		outputFile, err := build(target, version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error building for %s/%s: %v\n", target.OS, target.Arch, err)
			os.Exit(1)
		}
		builtFiles = append(builtFiles, outputFile)
		fmt.Printf("Built: %s\n", outputFile)
	}

	// Generate checksums
	checksumFile := filepath.Join(distDir, fmt.Sprintf("%s_%s_checksums.txt", binaryName, version))
	if err := generateChecksums(builtFiles, checksumFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating checksums: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated checksums: %s\n", checksumFile)

	// Output the version for use by the workflow
	fmt.Printf("\nVERSION=%s\n", version)
}

// isValidSemver checks if a version string is valid semver format
func isValidSemver(version string) bool {
	re := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	return re.MatchString(version)
}

// getLatestTag retrieves the latest semver tag from git
func getLatestTag() (string, error) {
	cmd := exec.Command("git", "tag", "-l", "v*")
	output, err := cmd.Output()
	if err != nil {
		return "", nil // No tags found, return empty
	}

	tags := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(tags) == 0 || (len(tags) == 1 && tags[0] == "") {
		return "", nil // No tags found
	}

	// Filter valid semver tags and sort them
	var validTags []string
	for _, tag := range tags {
		if isValidSemver(tag) {
			validTags = append(validTags, tag)
		}
	}

	if len(validTags) == 0 {
		return "", nil
	}

	// Sort tags by version
	sort.Slice(validTags, func(i, j int) bool {
		return compareSemver(validTags[i], validTags[j]) < 0
	})

	return validTags[len(validTags)-1], nil
}

// parseSemver parses a version string into major, minor, patch components
func parseSemver(version string) (int, int, int, error) {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return major, minor, patch, nil
}

// compareSemver compares two semver strings, returns -1, 0, or 1.
// Note: This function assumes inputs have already been validated as valid semver
// (via isValidSemver). Parse errors are ignored and will result in zero values.
func compareSemver(a, b string) int {
	aMajor, aMinor, aPatch, _ := parseSemver(a)
	bMajor, bMinor, bPatch, _ := parseSemver(b)

	if aMajor != bMajor {
		if aMajor < bMajor {
			return -1
		}
		return 1
	}
	if aMinor != bMinor {
		if aMinor < bMinor {
			return -1
		}
		return 1
	}
	if aPatch != bPatch {
		if aPatch < bPatch {
			return -1
		}
		return 1
	}
	return 0
}

// bumpVersion increments the version based on the bump type
func bumpVersion(bumpType string) (string, error) {
	// Validate bump type first
	bumpType = strings.ToLower(bumpType)
	if bumpType != "major" && bumpType != "minor" && bumpType != "patch" {
		return "", fmt.Errorf("invalid bump type: %s (use major, minor, or patch)", bumpType)
	}

	latestTag, err := getLatestTag()
	if err != nil {
		return "", err
	}

	// If no tags exist, start from default version
	if latestTag == "" {
		fmt.Println("No existing tags found, starting from default version")
		return defaultVersion, nil
	}

	major, minor, patch, err := parseSemver(latestTag)
	if err != nil {
		return "", err
	}

	switch bumpType {
	case "major":
		major++
		minor = 0
		patch = 0
	case "minor":
		minor++
		patch = 0
	case "patch":
		patch++
	}

	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}

// build compiles the binary for a specific target
func build(target Target, version string) (string, error) {
	// Determine output filename
	outputName := fmt.Sprintf("%s-%s-%s", binaryName, target.OS, target.Arch)
	if target.OS == "windows" {
		outputName += ".exe"
	}
	outputPath := filepath.Join(distDir, outputName)

	// Set up build command with ldflags for version
	// -s -w strips debug symbols and DWARF information for smaller binaries
	// -X main.Version=... injects version at build time (optional: add "var Version string" to main.go to use)
	ldflags := fmt.Sprintf("-s -w -X main.Version=%s", version)
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", outputPath, ".")

	// Set environment variables for cross-compilation
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOOS=%s", target.OS),
		fmt.Sprintf("GOARCH=%s", target.Arch),
		"CGO_ENABLED=0",
	)

	// Run the build
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build failed: %v\n%s", err, string(output))
	}

	return outputPath, nil
}

// generateChecksums creates a SHA256 checksums file for all built binaries
func generateChecksums(files []string, outputPath string) error {
	var checksums []string

	for _, file := range files {
		checksum, err := calculateSHA256(file)
		if err != nil {
			return fmt.Errorf("failed to calculate checksum for %s: %v", file, err)
		}
		// Use just the filename, not the full path
		checksums = append(checksums, fmt.Sprintf("%s  %s", checksum, filepath.Base(file)))
	}

	// Sort checksums for consistent output
	sort.Strings(checksums)

	// Write checksums to file
	content := strings.Join(checksums, "\n") + "\n"
	return os.WriteFile(outputPath, []byte(content), 0644)
}

// calculateSHA256 calculates the SHA256 hash of a file
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
