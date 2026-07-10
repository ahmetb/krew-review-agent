package tools

import (
	"fmt"
	"regexp"
)

// pluginNameRe defines the strict kebab-case plugin name format accepted by
// fetch_plugin_manifest: lowercase alphanumeric segments separated by single
// hyphens. This rejects path separators, traversal sequences, uppercase, and
// other characters that could escape the plugins/ directory.
var pluginNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// maxPluginNameLen bounds the accepted plugin name length.
const maxPluginNameLen = 64

// ValidatePluginName validates that name is a safe kebab-case plugin name. It
// rejects empty names, names with path separators or traversal sequences, and
// names that don't match the kebab-case pattern. This prevents the LLM from
// supplying arbitrary paths (see AGENT_CLI.md §13).
func ValidatePluginName(name string) error {
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}
	if len(name) > maxPluginNameLen {
		return fmt.Errorf("plugin name too long (%d > %d)", len(name), maxPluginNameLen)
	}
	if !pluginNameRe.MatchString(name) {
		return fmt.Errorf("plugin name %q must be lowercase kebab-case (a-z0-9 and single hyphens)", name)
	}
	return nil
}
