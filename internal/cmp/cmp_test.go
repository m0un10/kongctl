package cmp

import (
	"strings"
	"testing"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
)

func lookup(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestFromEnv(t *testing.T) {
	cfg := config.Defaults()
	cwd := t.TempDir()
	env := map[string]string{
		"ARGOCD_ENV_ENV_NAME":      "dev",
		"ARGOCD_ENV_SECRET_NAME":   "cfg",
		"ARGOCD_ENV_KIND":          "ExternalSecret",
		"ARGOCD_ENV_SKIP_LINT":     "true",
		"ARGOCD_ENV_FAKE_SECRETS":  "true",
		"ARGOCD_ENV_CONFIG_SCRIPT": "scripts/kong-config.sh",
		"ARGOCD_ENV_CONFIG_FILE":   "ops/kongctl.yaml",
		"CONSUMER_X_KEY":           "secret",
	}
	o, err := FromEnv(lookup(env), cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if o.Env != "dev" || o.RepoRoot != cwd || cfg.Manifest.Name != "cfg" || cfg.Manifest.Kind != "ExternalSecret" || !cfg.Lint.Skip {
		t.Fatalf("mapping wrong: %+v %+v", o, cfg.Manifest)
	}
	if o.FakeSecrets {
		t.Fatal("prefixed FAKE_SECRETS must be ignored")
	}
	if !o.IgnoredFakeParam || !o.DeprecatedConfigScript || o.ConfigFile != "ops/kongctl.yaml" {
		t.Fatalf("advisories wrong: %+v", o)
	}
	env["FAKE_SECRETS"] = "true"
	o, _ = FromEnv(lookup(env), config.Defaults(), cwd)
	if !o.FakeSecrets {
		t.Fatal("bare FAKE_SECRETS must be honoured")
	}
}

func TestFromEnvErrors(t *testing.T) {
	cwd := t.TempDir()
	if _, err := FromEnv(lookup(map[string]string{}), config.Defaults(), cwd); err == nil || kerr.ExitCode(err) != 2 || !strings.Contains(err.Error(), "ENV_NAME") {
		t.Fatalf("missing env: %v", err)
	}
	if _, err := FromEnv(lookup(map[string]string{"ARGOCD_ENV_ENV_NAME": "dev", "ARGOCD_ENV_BASEPATH": "nope/"}), config.Defaults(), cwd); err == nil || !strings.Contains(err.Error(), "basepath") {
		t.Fatalf("bad basepath: %v", err)
	}
	if _, err := FromEnv(lookup(map[string]string{"ARGOCD_ENV_ENV_NAME": "dev", "ARGOCD_ENV_SKIP_LINT": "yes-please"}), config.Defaults(), cwd); err == nil {
		t.Fatal("bad bool accepted")
	}
}

func TestCustomPrefixAndParams(t *testing.T) {
	cfg := config.Defaults()
	cfg.CMP.Prefix = "PLUGIN_"
	cfg.CMP.Params["env"] = "TARGET"
	o, err := FromEnv(lookup(map[string]string{"PLUGIN_TARGET": "sit"}), cfg, t.TempDir())
	if err != nil || o.Env != "sit" {
		t.Fatalf("custom mapping: %+v %v", o, err)
	}
}
