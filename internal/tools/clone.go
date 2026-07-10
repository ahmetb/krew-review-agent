package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// KrewIndexURL is the upstream krew-index repository cloned by get_all_existing_plugins
// and fetch_plugin_manifest.
const KrewIndexURL = "https://github.com/kubernetes-sigs/krew-index"

// KrewIndexDirName is the subdirectory (under the OS temp dir) where the clone
// lives.
const KrewIndexDirName = "krew-index"

// GitCloner clones url into dir. It is injectable for testing.
type GitCloner func(ctx context.Context, url, dir string) error

// DefaultGitCloner runs a shallow `git clone --depth 1`.
func DefaultGitCloner(ctx context.Context, url, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %w: %s", url, err, string(out))
	}
	return nil
}

// Clone lazily clones the krew-index repository and reuses the resulting
// directory for the lifetime of the process.
//
// Concurrency: the first call to Ensure performs the clone; concurrent and
// subsequent callers block until it completes, then reuse the directory. No
// re-clone is performed within a process lifetime. The clone is a shallow
// `git clone --depth 1` and is read-only after creation.
type Clone struct {
	url string
	dir string
	git GitCloner

	once      sync.Once
	cloneErr  error
	cloneDone bool
}

// NewClone creates a Clone rooted at baseDir/KrewIndexDirName cloning url.
// If git is nil, DefaultGitCloner is used.
func NewClone(baseDir, url string, git GitCloner) *Clone {
	if git == nil {
		git = DefaultGitCloner
	}
	return &Clone{
		url: url,
		dir: filepath.Join(baseDir, KrewIndexDirName),
		git: git,
	}
}

// DefaultKrewIndexClone returns a Clone rooted at os.TempDir()/krew-index that
// clones the upstream krew-index repo with the real git command.
func DefaultKrewIndexClone() *Clone {
	return NewClone(os.TempDir(), KrewIndexURL, nil)
}

// CloneForTest creates a Clone with an explicit base directory and git cloner,
// for use in tests.
func CloneForTest(baseDir, url string, git GitCloner) *Clone {
	return NewClone(baseDir, url, git)
}

// Path returns the absolute clone directory path. It is valid only after a
// successful Ensure.
func (c *Clone) Path() string {
	return c.dir
}

// Ensure clones the repository on first call and returns nil on success. After
// the first successful call, subsequent calls are no-ops. If the first clone
// fails, the error is cached and returned by all subsequent calls (no retry
// within the process lifetime).
func (c *Clone) Ensure(ctx context.Context) error {
	c.once.Do(func() {
		if _, err := os.Stat(c.dir); err == nil {
			// Directory already exists (e.g. from a previous process run in
			// the same temp dir). Reuse it without re-cloning.
			c.cloneDone = true
			return
		}
		if err := c.git(ctx, c.url, c.dir); err != nil {
			c.cloneErr = err
			return
		}
		c.cloneDone = true
	})
	if !c.cloneDone && c.cloneErr != nil {
		return c.cloneErr
	}
	if !c.cloneDone {
		return fmt.Errorf("krew-index clone not completed")
	}
	return nil
}

// Done reports whether Ensure has completed successfully. Primarily for tests.
func (c *Clone) Done() bool {
	return c.cloneDone
}

// PluginsDir returns the path to the plugins directory within the clone.
func (c *Clone) PluginsDir() string {
	return filepath.Join(c.dir, "plugins")
}

// ManifestPath returns the manifest file path for a given plugin name. It does
// not validate the name; callers must do so.
func (c *Clone) ManifestPath(name string) string {
	return filepath.Join(c.PluginsDir(), name+".yaml")
}
