package docker

// Fork addition (plan 43, review B2): the start-log fixtures were once named
// *.log, which .gitignore hides: the tests passed here and failed on a fresh
// checkout. Every testdata folder under src/ must hold files, and none of its
// files may match a name pattern of .gitignore.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestdataIsCommitted(t *testing.T) {
	ignore, err := os.Open("../../.gitignore")
	if err != nil {
		t.Skip("no .gitignore")
	}
	defer ignore.Close()
	patterns := []string{}
	scan := bufio.NewScanner(ignore)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		// name patterns only (they apply in every folder): "*.log", not "/main.tf" or "build/"
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") || strings.Contains(line, "/") {
			continue
		}
		patterns = append(patterns, line)
	}
	folders := 0
	filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || info.Name() != "testdata" {
			return nil
		}
		folders++
		files := 0
		filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			files++
			for _, pattern := range patterns {
				if ok, _ := filepath.Match(pattern, fi.Name()); ok {
					t.Errorf("%s matches %q in .gitignore: it would not be committed", p, pattern)
				}
			}
			return nil
		})
		if files == 0 {
			t.Errorf("%s is empty", path)
		}
		return nil
	})
	if folders == 0 {
		t.Fatal("no testdata folder found under src/")
	}
}
