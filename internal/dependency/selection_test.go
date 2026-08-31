package dependency

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func TestFilterSelectsDependenciesByNameTypeAndFile(t *testing.T) {
	projectDirectory := t.TempDir()
	rootPath := filepath.Join(projectDirectory, "package.json")
	workspacePath := filepath.Join(projectDirectory, "packages", "app", "package.json")
	dependencies := []shared.Dependency{
		{BaseDependency: shared.BaseDependency{Name: "react", Type: shared.Dependencies, FilePath: rootPath}},
		{BaseDependency: shared.BaseDependency{Name: "typescript", Type: shared.DevDependencies, FilePath: rootPath}},
		{BaseDependency: shared.BaseDependency{Name: "react", Type: shared.DevDependencies, FilePath: workspacePath}},
		{BaseDependency: shared.BaseDependency{Name: "vite", Type: shared.DevDependencies, FilePath: workspacePath}},
	}

	tests := []struct {
		name      string
		selectors []Selector
		expected  []shared.Dependency
	}{
		{name: "all", expected: dependencies},
		{name: "package name", selectors: []Selector{{Name: "react"}}, expected: []shared.Dependency{dependencies[0], dependencies[2]}},
		{name: "dependency type", selectors: []Selector{{Type: "devDependencies"}}, expected: dependencies[1:]},
		{name: "relative file", selectors: []Selector{{FilePath: "packages/app/package.json"}}, expected: dependencies[2:]},
		{name: "combined fields", selectors: []Selector{{Name: "react", Type: "devDependencies", FilePath: workspacePath}}, expected: []shared.Dependency{dependencies[2]}},
		{name: "selector union", selectors: []Selector{{Name: "typescript"}, {Name: "vite"}}, expected: []shared.Dependency{dependencies[1], dependencies[3]}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := Filter(dependencies, test.selectors, projectDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("Filter() = %+v, expected %+v", actual, test.expected)
			}
		})
	}
}

func TestFilterRejectsInvalidAndUnmatchedSelectors(t *testing.T) {
	dependencies := []shared.Dependency{{BaseDependency: shared.BaseDependency{Name: "react", Type: shared.Dependencies, FilePath: "/project/package.json"}}}
	for _, selectors := range [][]Selector{
		{{}},
		{{Type: "optionalDependencies"}},
		{{Name: "missing"}},
	} {
		if _, err := Filter(dependencies, selectors, "/project"); err == nil {
			t.Fatalf("Filter() accepted invalid selectors: %+v", selectors)
		}
	}
}
