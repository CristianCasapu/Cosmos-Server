//go:build ignore

// Fork addition: writes src/certs/providers.json, the DNS providers lego can
// answer a certificate challenge through and the variables each one takes,
// with lego's own description of every variable. It reads the .toml file
// lego keeps next to each provider, from the copy of lego the build uses
// (the version in go.mod), so the catalogue cannot name a variable the
// library does not read.
//
// Run it from the repository's root after lego's version changes in go.mod:
//
//	go run scripts/fork-lego-providers.go
//
// The server uses the catalogue as the list of variables a domain may hand
// to its provider (src/certs/variables.go) and the panel shows it when a
// domain is added (GET /api/certificates/providers). A test in src/certs
// fails when the catalogue and the library disagree.
package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type variable struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type provider struct {
	Name        string     `json:"name"`
	Credentials []variable `json:"credentials"`
	Optional    []variable `json:"optional"`
}

// variableName is what the name of an environment variable looks like.
var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// leftOut are the providers a server must never be told to use: "exec" runs
// a program named by a variable, "manual" waits for someone at a keyboard.
var leftOut = map[string]bool{"exec": true, "manual": true}

func main() {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/go-acme/lego/v5").Output()
	if err != nil {
		log.Fatal("cannot find lego: ", err)
	}
	files, err := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "providers", "dns", "*", "*.toml"))
	if err != nil || len(files) == 0 {
		log.Fatal("no provider descriptions found")
	}
	sort.Strings(files)

	catalogue := map[string]provider{}
	for _, path := range files {
		code, entry := read(path)
		if code == "" {
			log.Fatal("no Code in ", path)
		}
		if leftOut[code] {
			continue
		}
		catalogue[code] = entry
	}

	encoded, err := json.MarshalIndent(catalogue, "", " ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("src/certs/providers.json", append(encoded, '\n'), 0644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d providers", len(catalogue))
}

// read takes what is needed out of one description: the top-level Name and
// Code, and the two tables of variables. The files are plain enough for a
// line reader (KEY = "text"), which also keeps the variables in lego's order.
func read(path string) (string, provider) {
	file, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	code := ""
	entry := provider{Credentials: []variable{}, Optional: []variable{}}
	section := ""
	multiline := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if multiline != "" {
			// inside a ''' or """ text (the examples and notes): not ours
			if strings.Contains(line, multiline) {
				multiline = ""
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		for _, quote := range []string{"'''", `"""`} {
			if strings.HasPrefix(value, quote) && !strings.Contains(value[3:], quote) {
				multiline = quote
			}
		}
		if multiline != "" {
			continue
		}
		value = strings.Trim(value, `"'`)
		if section != "" && !variableName.MatchString(key) {
			// a line of prose in lego's table ("Application Default Credentials"), not a variable
			continue
		}
		switch section {
		case "":
			if key == "Name" {
				entry.Name = value
			}
			if key == "Code" {
				code = value
			}
		case "Configuration.Credentials":
			entry.Credentials = append(entry.Credentials, variable{Name: key, Text: value})
		case "Configuration.Additional":
			entry.Optional = append(entry.Optional, variable{Name: key, Text: value})
		}
	}
	return code, entry
}
