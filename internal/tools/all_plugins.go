package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// GetAllPlugins implements the get_all_existing_plugins tool (the "fat tool").
type GetAllPlugins struct {
	clone *Clone
}

// NewGetAllPlugins builds a get_all_existing_plugins tool backed by the given
// krew-index clone.
func NewGetAllPlugins(clone *Clone) *GetAllPlugins {
	return &GetAllPlugins{clone: clone}
}

func (t *GetAllPlugins) Name() string   { return "get_all_existing_plugins" }
func (t *GetAllPlugins) Terminal() bool { return false }

// pluginManifest captures the metadata.name, spec.shortDescription and
// spec.description fields from a krew plugin manifest YAML file. Krew
// manifests nest these fields under metadata/spec rather than at the top
// level, so the struct mirrors that layout.
type pluginManifest struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		ShortDescription string `yaml:"shortDescription"`
		Description      string `yaml:"description"`
	} `yaml:"spec"`
}

// Run clones (if needed), reads every plugins/*.yaml manifest, and returns a
// compiled "name: shortDescription | description" listing (description
// newlines are collapsed to spaces; the " | " separator is omitted when
// description is empty).
func (t *GetAllPlugins) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	if args != "" && args != "null" && args != "{}" {
		var v map[string]any
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return "", fmt.Errorf("get_all_existing_plugins expects no parameters: %w", err)
		}
	}
	if err := t.clone.Ensure(ctx); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(t.clone.PluginsDir())
	if err != nil {
		return "", fmt.Errorf("listing plugins directory: %w", err)
	}

	var sb strings.Builder
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(t.clone.PluginsDir(), e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var m pluginManifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			// Skip manifests that don't parse; don't fail the whole listing.
			continue
		}
		if m.Metadata.Name == "" {
			continue
		}
		desc := strings.ReplaceAll(m.Spec.Description, "\n", " ")
		if desc != "" {
			fmt.Fprintf(&sb, "%s: %s | %s\n", m.Metadata.Name, m.Spec.ShortDescription, desc)
		} else {
			fmt.Fprintf(&sb, "%s: %s\n", m.Metadata.Name, m.Spec.ShortDescription)
		}
		count++
	}
	if count == 0 {
		return "(no plugins found)", nil
	}
	return sb.String(), nil
}
