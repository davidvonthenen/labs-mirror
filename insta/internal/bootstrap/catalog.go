// Package bootstrap downloads lab sources and publishes isolated workspaces.
package bootstrap

import "fmt"

// Repository is maintainer configuration, not a per-invocation source override.
// Tag takes precedence over Branch; an empty Branch defaults to main.
// APIURL and ArchiveURL are optional test overrides, not user configuration.
type Repository struct {
	Origin     string `json:"origin"`
	Branch     string `json:"branch,omitempty"`
	Tag        string `json:"tag,omitempty"`
	APIURL     string `json:"-"`
	ArchiveURL string `json:"-"`
}

// DefaultRepository follows the repository explicitly selected by the user.
var DefaultRepository = Repository{
	Origin: "https://github.com/davidvonthenen/labs-mirror",
	Branch: "main",
	Tag:    "", // e.g. "v1.0.0"; takes priority over Branch when set.
}

type Lab struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SourcePath string `json:"source_path"`
}

func Catalog() []Lab {
	return []Lab{
		{ID: "rag-retrieval", Name: "RAG Retrieval", SourcePath: "rag-retrieval"},
		{ID: "agentic-rag", Name: "Agentic RAG", SourcePath: "agentic-rag"},
	}
}

func Lookup(id string) (Lab, error) {
	for _, lab := range Catalog() {
		if lab.ID == id {
			return lab, nil
		}
	}
	return Lab{}, fmt.Errorf("unknown lab %q; run insta lab list", id)
}
