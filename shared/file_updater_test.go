package shared

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type testPatternProvider struct{}

func (testPatternProvider) GetPattern(dependency OutdatedDependency) string {
	return `("` + regexp.QuoteMeta(dependency.Name) + `"\s*:\s*")([^"]*)"`
}

func (testPatternProvider) GetReplacement(_ OutdatedDependency, newVersion string) string {
	return `${1}` + newVersion + `"`
}

func TestPrepareDependenciesInFileRejectsStaleVersionWithoutWriting(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	original := "{\n  \"dependencies\": {\n    \"example\": \"^1.1.0\"\n  }\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareDependenciesInFile(filePath, []OutdatedDependency{{
		BaseDependency: BaseDependency{
			Name:            "example",
			OriginalVersion: "^1.0.0",
			LineNumber:      3,
		},
		LatestVersion: "1.2.0",
	}}, testPatternProvider{})
	if err == nil || !strings.Contains(err.Error(), "changed on line") {
		t.Fatalf("expected stale version error, got %v", err)
	}

	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatal("file changed after validation failure")
	}
}

func TestPreparedFileUpdatePreservesModeAndConstraint(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(filePath, []byte("\"example\": \">=1.0.0 <2.0.0\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareDependenciesInFile(filePath, []OutdatedDependency{{
		BaseDependency: BaseDependency{
			Name:            "example",
			OriginalVersion: ">=1.0.0 <2.0.0",
			LineNumber:      1,
		},
		LatestVersion: "1.9.0",
	}}, testPatternProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Apply(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "\"example\": \">=1.9.0 <2.0.0\"\n" {
		t.Fatalf("unexpected content: %s", data)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, expected 640", info.Mode().Perm())
	}
}

func TestPrepareRejectsUnsatisfiedUpdatedConstraint(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "package.json")
	original := "\"example\": \">=1.21.0 <2.0.0\"\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareDependenciesInFile(filePath, []OutdatedDependency{{
		BaseDependency: BaseDependency{Name: "example", OriginalVersion: ">=1.21.0 <2.0.0", LineNumber: 1},
		LatestVersion:  "3.0.0",
	}}, testPatternProvider{})
	if err == nil || !strings.Contains(err.Error(), "does not satisfy updated constraint") {
		t.Fatalf("expected constraint validation error, got %v", err)
	}
	content, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatal("file changed after invalid update")
	}
}

func TestPreparedFileUpdatePreservesSymlink(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "actual-package.json")
	symlinkPath := filepath.Join(directory, "package.json")
	if err := os.WriteFile(targetPath, []byte("\"example\": \"^1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(targetPath), symlinkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	prepared, err := PrepareDependenciesInFile(symlinkPath, []OutdatedDependency{{
		BaseDependency: BaseDependency{Name: "example", OriginalVersion: "^1.0.0", LineNumber: 1},
		LatestVersion:  "1.1.0",
	}}, testPatternProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Apply(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dependency file symlink was replaced")
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "\"example\": \"^1.1.0\"\n" {
		t.Fatalf("target content = %q", content)
	}
}

func TestPrepareRejectsHardLinkedFile(t *testing.T) {
	directory := t.TempDir()
	filePath := filepath.Join(directory, "package.json")
	otherPath := filepath.Join(directory, "package-copy.json")
	if err := os.WriteFile(filePath, []byte("\"example\": \"^1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filePath, otherPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	_, err := PrepareDependenciesInFile(filePath, []OutdatedDependency{{
		BaseDependency: BaseDependency{Name: "example", OriginalVersion: "^1.0.0", LineNumber: 1},
		LatestVersion:  "1.1.0",
	}}, testPatternProvider{})
	if err == nil || !strings.Contains(err.Error(), "hard-linked") {
		t.Fatalf("expected hard-link error, got %v", err)
	}
}
