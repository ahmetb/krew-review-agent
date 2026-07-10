package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// FetchPluginManifest implements the fetch_plugin_manifest tool.
type FetchPluginManifest struct {
	clone *Clone
}

// NewFetchPluginManifest builds a fetch_plugin_manifest tool reading from the
// given krew-index clone.
func NewFetchPluginManifest(clone *Clone) *FetchPluginManifest {
	return &FetchPluginManifest{clone: clone}
}

func (t *FetchPluginManifest) Name() string   { return "fetch_plugin_manifest" }
func (t *FetchPluginManifest) Terminal() bool { return false }

// Run reads the master manifest for the named plugin. For new-plugin
// submissions the file does not exist and a clear message is returned so the
// LLM relies on the diff instead.
func (t *FetchPluginManifest) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parsing fetch_plugin_manifest args: %w", err)
	}
	if err := ValidatePluginName(p.Name); err != nil {
		return "", err
	}
	if err := t.clone.Ensure(ctx); err != nil {
		return "", err
	}
	path := t.clone.ManifestPath(p.Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf(
				"Plugin '%s' does not exist in the master krew-index (this appears to be a new submission). "+
					"The full manifest content is available in the PR diff.", p.Name), nil
		}
		return "", fmt.Errorf("reading manifest for %q: %w", p.Name, err)
	}
	return string(data), nil
}
