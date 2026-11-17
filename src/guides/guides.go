// Package guides is a fork addition (plan 50): the "How to use it" guides of
// apps, one Markdown file per kind of app (s3.md: any S3-compatible storage).
//
// The panel renders a guide's text itself (client/src/utils/guides.js imports
// the same files); the server only reads each file's header, to say which
// guide an app gets (GET /api/dashboard, ForkGuide), in this order:
//
//  1. the URL's own setting (ForkInterface.Guide): a kind, or "none";
//  2. the recipe of the container's image (src/anyimage/recipes.json,
//     "provides"), against each guide's "provides";
//  3. the image's repository among a guide's "images".
//
// A new guide is a new file here with its header; nothing else changes.
package guides

import (
	"bufio"
	"embed"
	"regexp"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/anyimage"
)

//go:embed *.md
var files embed.FS

// Where a guide comes from.
const (
	FromSetting = "set"
	FromRecipe  = "recipe"
	FromImage   = "image"
)

// None is the setting that hides every guide of an app.
const None = "none"

// Guide is the header of one guide file.
type Guide struct {
	Kind     string
	Title    string
	Provides string
	Images   []string
	Region   string
	Regions  map[string]string // repository -> region
}

var (
	kindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
	known       = map[string]Guide{}
)

func init() {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		data, err := files.ReadFile(entry.Name())
		if err != nil {
			panic(err)
		}
		guide, ok := parseHeader(string(data))
		if !ok || guide.Kind+".md" != entry.Name() {
			panic("guides: the header of " + entry.Name() + " is not valid")
		}
		known[guide.Kind] = guide
	}
}

// parseHeader reads the "---" block that opens a guide.
func parseHeader(text string) (Guide, bool) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Guide{}, false
	}
	guide := Guide{Regions: map[string]string{}}
	closed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			closed = true
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return Guide{}, false
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "kind":
			guide.Kind = value
		case "title":
			guide.Title = value
		case "provides":
			guide.Provides = value
		case "images":
			guide.Images = strings.Fields(strings.ToLower(value))
		case "region":
			guide.Region = value
		case "regions":
			for _, pair := range strings.Fields(value) {
				image, region, ok := strings.Cut(pair, "=")
				if ok {
					guide.Regions[strings.ToLower(image)] = region
				}
			}
		}
	}
	return guide, closed && kindPattern.MatchString(guide.Kind) && guide.Title != ""
}

// Known reports whether a kind is one of the guides.
func Known(kind string) bool {
	_, ok := known[kind]
	return ok
}

// Kinds lists the guides, sorted.
func Kinds() []string {
	out := make([]string, 0, len(known))
	for kind := range known {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// ValidSetting reports whether a URL's setting is acceptable: "" (as
// proposed), "none" or a known kind.
func ValidSetting(setting string) bool {
	return setting == "" || setting == None || Known(setting)
}

// Repository is an image's repository as the guides and the recipes name it
// ("rustfs/rustfs" for "docker.io/rustfs/rustfs:1.0.5@sha256:…"), "" when
// it is not an image reference.
func Repository(image string) string {
	image = strings.TrimSpace(image)
	if image == "" || len(image) > 512 {
		return ""
	}
	ref, err := anyimage.Parse(image, func(string) bool { return true })
	if err != nil {
		return ""
	}
	return strings.ToLower(ref.Name())
}

// For is the guide of an app: the URL's setting, else what the image is.
// image is the container's image ("" when the URL leads to no container).
func For(setting string, image string) (kind string, source string) {
	if setting == None {
		return "", ""
	}
	if setting != "" && Known(setting) {
		return setting, FromSetting
	}
	if image == "" {
		return "", ""
	}
	if recipe := anyimage.RecipeForImage(image); recipe != nil && recipe.Provides != "" {
		for _, kind := range Kinds() {
			if known[kind].Provides == recipe.Provides {
				return kind, FromRecipe
			}
		}
	}
	repository := Repository(image)
	if repository == "" {
		return "", ""
	}
	for _, kind := range Kinds() {
		for _, candidate := range known[kind].Images {
			if candidate == repository {
				return kind, FromImage
			}
		}
	}
	return "", ""
}

// Region is the region a guide proposes for an image ("" when it has none).
func Region(kind string, image string) string {
	guide, ok := known[kind]
	if !ok {
		return ""
	}
	if region, ok := guide.Regions[Repository(image)]; ok {
		return region
	}
	return guide.Region
}
