package telegram

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// freeText reports whether the expression of a sentence's value can carry
// the text of an error or other free text: an error's Error(), a variable
// named err…, or a value of a free-text name.
func freeText(src string, name string) bool {
	if regexp.MustCompile(`(?i)\berr\w*\b|\.Error\(\)`).MatchString(src) {
		return true
	}
	switch name {
	case "reason", "summary", "message", "answer", "why", "text", "cause":
		return true
	}
	return false
}

// Review T3: every value of a kind's sentences that its emitters feed from
// an error or a free text is marked raw in the table (found in the code by
// shape, not by a hand list), so that it is guarded as an error text and
// left out with "Include error details" switched off.
func TestErrorTextValuesAreMarked(t *testing.T) {
	marked := map[string]map[string]bool{}
	for name, kt := range texts.Kinds {
		marked[name] = map[string]bool{}
		for _, v := range kt.Raw {
			marked[name][v] = true
		}
	}
	uses := func(kind string, v string) bool {
		kt := texts.Kinds[kind]
		for _, text := range []string{kt.Preview, kt.What, kt.WhatAuto, kt.Means, kt.Todo} {
			if strings.Contains(text, "{{"+v+"}}") {
				return true
			}
		}
		return false
	}
	fset := token.NewFileSet()
	checked := 0
	filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		file, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name != "NText" {
				return true
			}
			key, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			k := strings.Trim(key.Value, `"`)
			for i := 1; i+1 < len(call.Args); i += 2 {
				lit, ok := call.Args[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				v := strings.Trim(lit.Value, `"`)
				src := string(raw[fset.Position(call.Args[i+1].Pos()).Offset:fset.Position(call.Args[i+1].End()).Offset])
				if !freeText(src, v) {
					continue
				}
				for kind, kt := range texts.Kinds {
					if !(kt.WhatKey == k || strings.HasPrefix(k, "notif."+kind+".")) || !uses(kind, v) {
						continue
					}
					checked++
					if !marked[kind][v] {
						t.Errorf("%s: {{%s}} is fed from %q (%s) and is not marked raw", kind, v, src, filepath.Base(path))
					}
				}
			}
			return true
		})
		return nil
	})
	// the old notifications without sentences carry one free text
	sources, _ := os.ReadFile("sources.go")
	for _, m := range regexp.MustCompile(`withVar\("([a-z.]+)", "(\w+)"\)`).FindAllStringSubmatch(string(sources), -1) {
		checked++
		if uses(m[1], m[2]) && !marked[m[1]][m[2]] && !strings.Contains(string(sources), "if nameLike(vars) || rawVar(kind, name) {") {
			t.Errorf("%s: {{%s}} holds an old notification's free text and is neither marked raw nor shape-checked", m[1], m[2])
		}
	}
	if !marked["error.other"]["message"] {
		t.Error("error.other: {{message}} (a server error's text) is not marked raw")
	}
	if checked < 10 {
		t.Errorf("only %d values were checked: the scan finds nothing", checked)
	}
}

// With "Include error details" off no error text leaves in any value; on,
// an error text goes through the guard of error texts (a machine-made key
// is cut), in the values and in the error block.
func TestErrorTextsInValuesFollowTheSetting(t *testing.T) {
	key := "Zx9Kq2Lm8Np4Rs7Tv1Wy3Ab6CdEf5Gh0Jk"
	for name, kt := range texts.Kinds {
		if len(kt.Raw) == 0 {
			continue
		}
		nt := sampleNotice(name)
		nt.sample = false
		for _, v := range kt.Raw {
			nt.vars[v] = "/mnt/a: RAWERRTEXT " + key
		}
		nt.raw = "RAWERRTEXT " + key
		st := testSettings()
		if text, _ := textOf(t, st, nt); strings.Contains(text, key) {
			t.Errorf("%s: a machine-made key in an error text was sent: %s", name, text)
		}
		st.hideErrors = true
		if text, _ := textOf(t, st, nt); strings.Contains(text, "RAWERRTEXT") {
			t.Errorf("%s: error text sent with the details off: %s", name, text)
		}
	}
}
