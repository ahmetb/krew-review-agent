package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
)

// base64Encode helper for building Pub/Sub envelopes in tests.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// mkdirPlugins creates the plugins/ subdirectory inside a fake krew-index clone.
func mkdirPlugins(dir string) error {
	return os.MkdirAll(filepath.Join(dir, "plugins"), 0o755)
}
