package tools

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeCloner returns a GitCloner that materializes a fake krew-index layout
// (with the given plugin file contents) instead of running git.
func fakeCloner(t *testing.T, plugins map[string]string) GitCloner {
	t.Helper()
	return func(ctx context.Context, url, dir string) error {
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
			return err
		}
		for name, content := range plugins {
			path := filepath.Join(dir, "plugins", name+".yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}

func newCloneWithFake(t *testing.T, plugins map[string]string) *Clone {
	t.Helper()
	c := CloneForTest(t.TempDir(), KrewIndexURL, fakeCloner(t, plugins))
	t.Cleanup(func() { os.RemoveAll(c.Path()) })
	return c
}

func TestCloneEnsureClonesOnce(t *testing.T) {
	calls := 0
	c := CloneForTest(t.TempDir(), "u", func(ctx context.Context, url, dir string) error {
		calls++
		return os.MkdirAll(filepath.Join(dir, "plugins"), 0o755)
	})
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure2: %v", err)
	}
	if calls != 1 {
		t.Errorf("cloner called %d times, want 1", calls)
	}
	if !c.Done() {
		t.Errorf("Done=false")
	}
}

func TestCloneEnsureReusesExistingDir(t *testing.T) {
	calls := 0
	base := t.TempDir()
	// Pre-create the clone directory so the cloner should be skipped.
	if err := os.MkdirAll(filepath.Join(base, KrewIndexDirName, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := CloneForTest(base, "u", func(ctx context.Context, url, dir string) error {
		calls++
		return nil
	})
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if calls != 0 {
		t.Errorf("cloner called %d times, want 0 (reuse existing)", calls)
	}
}

func TestCloneEnsureCachesError(t *testing.T) {
	want := errFake("boom")
	c := CloneForTest(t.TempDir(), "u", func(ctx context.Context, url, dir string) error {
		return want
	})
	if err := c.Ensure(context.Background()); err == nil {
		t.Fatal("expected first error")
	}
	if err := c.Ensure(context.Background()); err == nil {
		t.Fatal("expected cached error on second call")
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func TestCloneConcurrentEnsure(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	c := CloneForTest(t.TempDir(), "u", func(ctx context.Context, url, dir string) error {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // simulate slow clone
		return os.MkdirAll(filepath.Join(dir, "plugins"), 0o755)
	})

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Ensure(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("cloner called %d times, want 1", calls)
	}
}

func TestCloneManifestPath(t *testing.T) {
	c := CloneForTest(t.TempDir(), "u", fakeCloner(t, nil))
	if got := c.ManifestPath("whoami"); !filepath.IsAbs(got) || filepath.Base(got) != "whoami.yaml" {
		t.Errorf("manifest path=%q", got)
	}
}
