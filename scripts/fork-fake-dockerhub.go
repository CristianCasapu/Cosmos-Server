//go:build ignore

// Fork addition (plan 40): a stand-in for Docker Hub's API, for
// scripts/fork-ui-check-any-image.mjs. Never the real Docker Hub.
//
// It answers, over HTTPS (CERT and KEY: a certificate for hub.docker.com
// signed by the test's own authority), the two calls Cosmos makes:
//   GET /v2/search/repositories/?query=…&page_size=…   from a fixed list
//   GET /v2/namespaces/{ns}/repositories/{repo}/tags     from the tags the
//       test registry at REGISTRY (https://registry-1.docker.io on the test
//       network, CA trusted through SSL_CERT_FILE) holds for that repository
//   GET /v2/namespaces/{ns}/repositories/{repo}/tags/{tag}  the tag's
//       platforms with their manifests' digests and sizes, read from that
//       registry's manifests (as Docker Hub's API answers them)
//   GET /v2/namespaces/{ns}/repositories/{repo}   the repository with its
//       full_description, from DESCRIPTIONS/{ns}_{repo}.md when set (plan 43)
// Run on the test's --internal network with the network alias hub.docker.com:
//   go run scripts/fork-fake-dockerhub.go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type repo struct {
	Name        string `json:"repo_name"`
	Description string `json:"short_description"`
	Stars       int64  `json:"star_count"`
	Pulls       int64  `json:"pull_count"`
	Official    bool   `json:"is_official"`
}

var catalogue = []repo{
	{"cimg/postgres", "", 9, 869601096, false},
	{"postgres", "The PostgreSQL object-relational database system provides reliability and data integrity.", 15056, 12455318364, true},
	{"bitnami/postgresql", "Bitnami container image for PostgreSQL, with a description long enough to wrap on a phone and show that nothing runs out of the dialog", 2400, 1000000000, false},
	{"redis", "Redis is the world's fastest data platform for caching, vector search, and NoSQL databases.", 13000, 9000000000, true},
	{"nginx", "Official build of Nginx.", 21000, 13000000000, true},
	{"busybox", "Busybox base image.", 3400, 9500000000, true},
	{"example/dbapp", "A small app that keeps its data in PostgreSQL (test image).", 3, 1200, false},
	// plan 45: the databases and web managers of scripts/fork-ui-check-credentials.mjs
	{"mariadb", "MariaDB Server is a high performing open source relational database, forked from MySQL.", 6000, 1000000000, true},
	{"dpage/pgadmin4", "pgAdmin 4 is a web based administration tool for the PostgreSQL database.", 1500, 300000000, false},
	{"phpmyadmin", "phpMyAdmin - A web interface for MySQL and MariaDB.", 1100, 500000000, true},
	{"adminer", "Database management in a single PHP file.", 1000, 200000000, true},
}

func main() {
	registry := os.Getenv("REGISTRY")
	client := &http.Client{Timeout: 10 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search/repositories/", func(w http.ResponseWriter, r *http.Request) {
		query := strings.ToLower(r.URL.Query().Get("query"))
		log.Printf("search %q", query)
		results := []repo{}
		for _, item := range catalogue {
			if strings.Contains(item.Name, query) || strings.Contains(strings.ToLower(item.Description), query) {
				results = append(results, item)
			}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"count": len(results), "results": results})
	})
	mux.HandleFunc("/v2/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v2/namespaces/"), "/")
		if len(parts) == 5 && parts[1] == "repositories" && parts[3] == "tags" {
			log.Printf("tag %s/%s:%s", parts[0], parts[2], parts[4])
			answer, ok := tagInfo(client, registry, parts[0]+"/"+parts[2], parts[4])
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(answer)
			return
		}
		// plan 43: the repository's description, from DESCRIPTIONS/<ns>_<repo>.md (src/anyimage/testdata/readmes)
		if len(parts) == 3 && parts[1] == "repositories" {
			log.Printf("description %s/%s", parts[0], parts[2])
			dir := os.Getenv("DESCRIPTIONS")
			text, err := os.ReadFile(dir + "/" + parts[0] + "_" + parts[2] + ".md")
			if dir == "" || err != nil || strings.ContainsAny(parts[0]+parts[2], "./\\") {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"namespace": parts[0], "name": parts[2], "full_description": string(text)})
			return
		}
		if len(parts) != 4 || parts[1] != "repositories" || parts[3] != "tags" {
			http.NotFound(w, r)
			return
		}
		log.Printf("tags %s/%s", parts[0], parts[2])
		res, err := client.Get(registry + "/v2/" + parts[0] + "/" + parts[2] + "/tags/list")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			http.NotFound(w, r)
			return
		}
		var list struct {
			Tags []string `json:"tags"`
		}
		json.NewDecoder(res.Body).Decode(&list)
		results := []map[string]string{}
		for _, tag := range list.Tags {
			results = append(results, map[string]string{"name": tag})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"count": len(results), "next": nil, "results": results})
	})
	log.Fatal(http.ListenAndServeTLS(":443", os.Getenv("CERT"), os.Getenv("KEY"), mux))
}

var accept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

type manifest struct {
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform *struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
}

func fetch(client *http.Client, address string, into interface{}) (string, bool) {
	req, _ := http.NewRequest("GET", address, nil)
	req.Header.Set("Accept", accept)
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", false
	}
	return res.Header.Get("Docker-Content-Digest"), json.NewDecoder(res.Body).Decode(into) == nil
}

func size(m manifest) int64 {
	var n int64
	for _, l := range m.Layers {
		n += l.Size
	}
	return n
}

// tagInfo answers like Docker Hub's API for one tag.
func tagInfo(client *http.Client, registry, repo, tag string) (map[string]interface{}, bool) {
	var top manifest
	digest, ok := fetch(client, registry+"/v2/"+repo+"/manifests/"+tag, &top)
	if !ok {
		return nil, false
	}
	images := []map[string]interface{}{}
	if len(top.Manifests) > 0 {
		for _, m := range top.Manifests {
			if m.Platform == nil {
				continue
			}
			var one manifest
			fetch(client, registry+"/v2/"+repo+"/manifests/"+m.Digest, &one)
			var variant interface{}
			if m.Platform.Variant != "" {
				variant = m.Platform.Variant
			}
			images = append(images, map[string]interface{}{"os": m.Platform.OS, "architecture": m.Platform.Architecture, "variant": variant, "digest": m.Digest, "size": size(one)})
		}
	} else {
		var config struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		}
		fetch(client, registry+"/v2/"+repo+"/blobs/"+top.Config.Digest, &config)
		images = append(images, map[string]interface{}{"os": config.OS, "architecture": config.Architecture, "variant": nil, "digest": digest, "size": size(top)})
	}
	return map[string]interface{}{"name": tag, "digest": digest, "images": images}, true
}
