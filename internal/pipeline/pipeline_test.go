package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/logx"
	"github.com/m0un10/kongctl/internal/placeholder"
)

func fixture(t *testing.T, values map[string]string) (*Pipeline, *bytes.Buffer) {
	t.Helper()
	fsys := os.DirFS("../../testdata/repo")
	cfg := config.Defaults()
	if err := cfg.LoadFile(fsys, "kongctl.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Check(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	return &Pipeline{FS: fsys, Config: cfg, Log: logx.New(&buf, logx.LevelDebug, "[t]"), Lookup: placeholder.MapLookup(values)}, &buf
}

func TestSecretManifestWithRealValues(t *testing.T) {
	p, _ := fixture(t, map[string]string{
		"CONSUMER_ALPHA_BASICAUTH_PASSWORD": "p@ss: \"word\"",
		"CONSUMER_ALPHA_KEYAUTH_KEY":        "key-1",
		"CONSUMER_BETA_KEYAUTH_KEY":         "key-2",
	})
	out, err := p.Manifest(context.Background(), "dev", false)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Kind string            `yaml:"kind"`
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "Secret" {
		t.Fatalf("kind = %s", doc.Kind)
	}
	decoded, err := base64.StdEncoding.DecodeString(doc.Data["kong.yml"])
	if err != nil || !strings.Contains(string(decoded), "p@ss") {
		t.Fatalf("data not base64 of rendered config: %v %q", err, decoded)
	}
}

func TestSecretManifestMissingValues(t *testing.T) {
	p, _ := fixture(t, nil)
	_, err := p.Manifest(context.Background(), "dev", false)
	if err == nil || kerr.ExitCode(err) != 1 || !strings.Contains(err.Error(), "CONSUMER_ALPHA_KEYAUTH_KEY") {
		t.Fatalf("expected config error naming placeholders, got %v", err)
	}
}

func TestExternalSecretManifestNeedsNoValues(t *testing.T) {
	p, log := fixture(t, nil)
	p.Config.Manifest.Kind = "ExternalSecret"
	out, err := p.Manifest(context.Background(), "prod", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	s := string(out)
	for _, want := range []string{
		"kind: ExternalSecret",
		"kind: ClusterSecretStore",
		"name: example-store",
		"token: {{ .MY_TOKEN | quote }}",
		"key: {{ .CONSUMER_ALPHA_KEYAUTH_KEY | quote }}",
		"key: SECRET_CONSUMER_ALPHA_KEYAUTH_KEY",
		"- secretKey: MY_TOKEN\n      remoteRef:\n        key: MY_TOKEN",
		"port: 8443", // patch applied inside the template
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "${") {
		t.Fatal("placeholder leaked into template")
	}
	if !strings.Contains(log.String(), "validate prod: ok") || !strings.Contains(log.String(), "lint prod: ok") {
		t.Fatalf("expected validate and lint to run on the fake render:\n%s", log)
	}
}

func TestTemplateEscapesLiteralBraces(t *testing.T) {
	p, _ := fixture(t, nil)
	data := map[string]any{"a": "${A}", "lit": "x {{ y }}", "emb": "h-${B}"}
	out, names, err := p.Template("dev", data)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `a: {{ .A | quote }}`) || !strings.Contains(s, `emb: h-{{ .B }}`) || !strings.Contains(s, `{{ "{{" }} y }}`) {
		t.Fatalf("template wrong:\n%s", s)
	}
	if strings.Join(names, ",") != "A,B" {
		t.Fatalf("names = %v", names)
	}
}

func TestCheckEnvFailures(t *testing.T) {
	p, log := fixture(t, nil)
	err := p.CheckEnv(context.Background(), "broken")
	if err == nil || kerr.ExitCode(err) != 1 {
		t.Fatalf("expected validate failure, got %v", err)
	}
	if !strings.Contains(log.String(), "entity already exists") {
		t.Fatalf("validate message not logged:\n%s", log)
	}
	if err := p.CheckEnv(context.Background(), "dev"); err != nil {
		t.Fatalf("dev should pass: %v\n%s", err, log)
	}
}

func TestLintAllowFailure(t *testing.T) {
	p, log := fixture(t, nil)
	// A config the ruleset rejects: http upstream.
	bad := []byte("_format_version: \"3.0\"\nservices:\n- host: h\n  name: s\n  port: 80\n  protocol: http\n")
	if err := p.Check(context.Background(), "x", bad); err == nil || kerr.ExitCode(err) != 1 {
		t.Fatalf("expected lint failure, got %v", err)
	}
	p.Config.Lint.AllowFailure = true
	if err := p.Check(context.Background(), "x", bad); err != nil {
		t.Fatalf("allowFailure should downgrade to a warning: %v", err)
	}
	if !strings.Contains(log.String(), "WARNING: lint findings") {
		t.Fatalf("expected warning:\n%s", log)
	}
}
