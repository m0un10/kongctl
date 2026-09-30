package compose

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/m0un10/kongctl/internal/layout"
)

func testLayout() layout.Layout {
	return layout.Layout{
		SourceDir:    "environments/{env}/config/kong",
		CommonDir:    "environments/_common/config/kong",
		PatchesDir:   "environments/{env}/config/kong.patches",
		Globs:        []string{"*.yml", "*.yaml"},
		EnvNameRegex: regexp.MustCompile(`^[a-z0-9-]+$`),
	}
}

func TestMergeConcatenatesArraysAndTracksVersion(t *testing.T) {
	a := Source{Name: "a.yaml", Data: []byte("_format_version: \"3.0\"\nservices:\n- name: a\n")}
	b := Source{Name: "b.yaml", Data: []byte("_format_version: \"3.1\"\nservices:\n- name: b\nconsumers:\n- username: c\n")}
	out, err := Merge([]Source{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out["services"].([]any)); got != 2 {
		t.Fatalf("services = %d, want 2", got)
	}
	if out["_format_version"] != "3.1" {
		t.Fatalf("_format_version = %v, want 3.1", out["_format_version"])
	}
	if _, ok := out["consumers"]; !ok {
		t.Fatal("consumers missing")
	}
}

func TestMergeRejectsMajorMismatch(t *testing.T) {
	a := Source{Name: "a.yaml", Data: []byte("_format_version: \"3.0\"\n")}
	b := Source{Name: "b.yaml", Data: []byte("_format_version: \"1.1\"\n")}
	if _, err := Merge([]Source{a, b}); err == nil {
		t.Fatal("expected incompatible version error")
	}
}

func TestComposeOrderCommonAndPatches(t *testing.T) {
	fsys := fstest.MapFS{
		"environments/_common/config/kong/00-global.yaml":   {Data: []byte("_format_version: \"3.0\"\nplugins:\n- name: prometheus\n")},
		"environments/dev/config/kong/b.yaml":               {Data: []byte("_format_version: \"3.0\"\nservices:\n- name: b\n  port: 80\n")},
		"environments/dev/config/kong/a.yml":                {Data: []byte("_format_version: \"3.0\"\nservices:\n- name: a\n  port: 80\n")},
		"environments/dev/config/kong/ignored.txt":          {Data: []byte("not yaml")},
		"environments/dev/config/kong.patches/10-port.yaml": {Data: []byte("patches:\n- selectors: [\"$.services[?(@.name=='a')]\"]\n  values:\n    port: 8443\n")},
	}
	res, err := Compose(fsys, testLayout(), "dev", Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{
		"environments/_common/config/kong/00-global.yaml",
		"environments/dev/config/kong/a.yml",
		"environments/dev/config/kong/b.yaml",
	}
	if strings.Join(res.Files, ",") != strings.Join(wantFiles, ",") {
		t.Fatalf("files = %v", res.Files)
	}
	if len(res.Patches) != 1 {
		t.Fatalf("patches = %v", res.Patches)
	}
	svcs := res.Data["services"].([]any)
	if svcs[0].(map[string]any)["port"] != float64(8443) {
		t.Fatalf("patch not applied: %v", svcs[0])
	}
	if svcs[1].(map[string]any)["port"] != float64(80) {
		t.Fatalf("patch leaked: %v", svcs[1])
	}
	if _, ok := res.Data["plugins"]; !ok {
		t.Fatal("common file not merged")
	}
	text, err := res.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(text), "_format_version: \"3.0\"\nplugins:\n- name: prometheus\nservices:\n- name: a\n  port: 8443\n") {
		t.Fatalf("canonical form unexpected:\n%s", text)
	}
}

func TestComposeErrors(t *testing.T) {
	fsys := fstest.MapFS{"environments/empty/config/kong/.keep": {Data: []byte("")}}
	if _, err := Compose(fsys, testLayout(), "missing", Options{}); !errors.Is(err, ErrNoSources) {
		t.Fatalf("missing dir: %v", err)
	}
	if _, err := Compose(fsys, testLayout(), "empty", Options{}); !errors.Is(err, ErrNoSources) {
		t.Fatalf("empty dir: %v", err)
	}
	if _, err := Compose(fsys, testLayout(), "../etc", Options{}); err == nil {
		t.Fatal("unsafe env name accepted")
	}
}
