package review

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/layout"
)

func testLayout() layout.Layout {
	return layout.Layout{
		SourceDir:    "environments/{env}/config/kong",
		Globs:        []string{"*.yml", "*.yaml"},
		EnvNameRegex: regexp.MustCompile(`^[a-z0-9-]+$`),
	}
}

const one = "_format_version: \"3.0\"\nservices:\n- name: a\n  port: 443\n  host: h\n"

func TestSplitFileIsNotASemanticChange(t *testing.T) {
	g := gitx.NewFake()
	g.AddRef("base", "b1", map[string][]byte{
		"environments/dev/config/kong/kong.yml": []byte(one),
	})
	g.AddRef("head", "h1", map[string][]byte{
		// same content, split in two files with different key order
		"environments/dev/config/kong/a.yaml": []byte("_format_version: \"3.0\"\nservices:\n- host: h\n  name: a\n  port: 443\n"),
	})
	r := Compare(context.Background(), g, testLayout(), "dev", Options{Base: "b1", HeadFS: gitx.NewFS(context.Background(), g, "h1")})
	if r.Status != StatusUnchanged || r.SemanticChanged() {
		t.Fatalf("expected no semantic change: %+v", r)
	}
	if !r.RawChanged() || !strings.Contains(r.Raw, "/dev/null") {
		t.Fatalf("raw diff should show the file rename: %+v", r)
	}
}

func TestRealChangeAndAddDelete(t *testing.T) {
	g := gitx.NewFake()
	g.AddRef("base", "b1", map[string][]byte{"environments/dev/config/kong/kong.yml": []byte(one)})
	g.AddRef("head", "h1", map[string][]byte{
		"environments/dev/config/kong/kong.yml": []byte(strings.Replace(one, "443", "8443", 1)),
		"environments/new/config/kong/kong.yml": []byte(one),
	})
	head := gitx.NewFS(context.Background(), g, "h1")
	r := Compare(context.Background(), g, testLayout(), "dev", Options{Base: "b1", HeadFS: head})
	if r.Status != StatusChanged || !strings.Contains(r.Semantic, "port: 8443") {
		t.Fatalf("expected semantic change: %+v", r)
	}
	if r := Compare(context.Background(), g, testLayout(), "new", Options{Base: "b1", HeadFS: head}); r.Status != StatusAdded {
		t.Fatalf("expected added: %+v", r)
	}
	if r := Compare(context.Background(), g, testLayout(), "gone", Options{Base: "b1", HeadFS: head}); r.Status != StatusAbsent {
		t.Fatalf("expected absent: %+v", r)
	}
	baseOnly := gitx.NewFake()
	baseOnly.AddRef("base", "b1", map[string][]byte{"environments/old/config/kong/kong.yml": []byte(one)})
	baseOnly.AddRef("head", "h1", map[string][]byte{"README.md": []byte("x")})
	if r := Compare(context.Background(), baseOnly, testLayout(), "old", Options{Base: "b1", HeadFS: gitx.NewFS(context.Background(), baseOnly, "h1")}); r.Status != StatusDeleted {
		t.Fatalf("expected deleted: %+v", r)
	}
}

func TestUnparseableSideIsError(t *testing.T) {
	g := gitx.NewFake()
	g.AddRef("base", "b1", map[string][]byte{"environments/dev/config/kong/kong.yml": []byte("key: {{ helm }}\n")})
	g.AddRef("head", "h1", map[string][]byte{"environments/dev/config/kong/kong.yml": []byte(one)})
	r := Compare(context.Background(), g, testLayout(), "dev", Options{Base: "b1", HeadFS: gitx.NewFS(context.Background(), g, "h1")})
	if r.Status != StatusError || r.Err == nil {
		t.Fatalf("expected error status: %+v", r)
	}
}
