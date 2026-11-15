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
