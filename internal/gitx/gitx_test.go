package gitx

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestExecAgainstRealRepo builds a small repository and exercises every
// method through the real git binary.
func TestExecAgainstRealRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("needs git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	src := filepath.Join(dir, "environments", "dev", "config", "kong")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "kong.yml"), []byte("v: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "base")
	base := run(t, dir, "rev-parse", "HEAD")
	run(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "kong.yml"), []byte("v: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extra.yaml"), []byte("w: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", ".")
	// leave the changes staged, uncommitted

	ctx := context.Background()
	g := &Exec{Dir: dir}
	if top, err := g.TopLevel(ctx); err != nil || filepath.Clean(top) != filepath.Clean(mustEval(t, dir)) {
		t.Fatalf("toplevel = %q %v", top, err)
	}
	if sha, err := g.RevParse(ctx, "main"); err != nil || sha != base {
		t.Fatalf("rev-parse = %q %v", sha, err)
	}
	if _, err := g.RevParse(ctx, "nope"); !IsNotFound(err) {
		t.Fatalf("missing ref should be not-found: %v", err)
	}
	if mb, err := g.MergeBase(ctx, "main", "HEAD"); err != nil || mb != base {
		t.Fatalf("merge-base = %q %v", mb, err)
	}
	if data, err := g.ReadBlob(ctx, base, "environments/dev/config/kong/kong.yml"); err != nil || string(data) != "v: 1\n" {
		t.Fatalf("blob at base = %q %v", data, err)
	}
	if data, err := g.ReadBlob(ctx, "", "environments/dev/config/kong/kong.yml"); err != nil || string(data) != "v: 2\n" {
		t.Fatalf("blob in index = %q %v", data, err)
	}
	if _, err := g.ReadBlob(ctx, base, "environments/dev/config/kong/extra.yaml"); !IsNotFound(err) {
		t.Fatalf("extra should not exist at base: %v", err)
	}
	entries, err := g.ListTree(ctx, "", "environments/dev/config/kong")
	if err != nil || len(entries) != 2 {
		t.Fatalf("index tree = %+v %v", entries, err)
	}
	entries, err = g.ListTree(ctx, base, "environments/dev/config/kong")
	if err != nil || len(entries) != 1 || entries[0].Name != "kong.yml" {
		t.Fatalf("base tree = %+v %v", entries, err)
	}
	staged, err := g.StagedPaths(ctx)
	if err != nil || len(staged) != 2 {
		t.Fatalf("staged = %v %v", staged, err)
	}
	if err := g.ConfigSet(ctx, "core.hooksPath", ".githooks"); err != nil {
		t.Fatal(err)
	}
	if run(t, dir, "config", "--local", "core.hooksPath") != ".githooks" {
		t.Fatal("config not set")
	}

	// gitfs over the index
	fsys := NewFS(ctx, g, "")
	names, err := fs.ReadDir(fsys, "environments/dev/config/kong")
	if err != nil || len(names) != 2 {
		t.Fatalf("gitfs readdir = %v %v", names, err)
	}
	if st, err := fs.Stat(fsys, "environments/dev"); err != nil || !st.IsDir() {
		t.Fatalf("gitfs stat dir: %v %v", st, err)
	}
	if _, err := fs.Stat(fsys, "environments/nope"); err == nil {
		t.Fatal("missing dir should not stat")
	}
	sha, fell, err := ResolveBase(ctx, g, "main", true)
	if err != nil || fell || sha != base {
		t.Fatalf("ResolveBase = %q %v %v", sha, fell, err)
	}
}

func mustEval(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
