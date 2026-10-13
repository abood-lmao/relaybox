package fixture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleFixturesAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no example fixtures found in examples/fixtures")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(data, 1<<20); err != nil {
				t.Fatal(err)
			}
		})
	}
}
