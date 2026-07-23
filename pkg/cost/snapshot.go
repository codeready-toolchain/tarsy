package cost

import (
	_ "embed"
	"fmt"
)

//go:embed snapshot.json
var snapshotJSON []byte

// loadSnapshot parses the bundled curated LiteLLM snapshot.
func loadSnapshot() (map[string]catalogEntry, error) {
	entries, err := parseCatalogJSON(snapshotJSON)
	if err != nil {
		return nil, fmt.Errorf("load bundled snapshot: %w", err)
	}
	return entries, nil
}
