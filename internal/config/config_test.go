package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func lookupFrom(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Check(); err != nil {
		t.Fatal(err)
	}
}

func TestPrecedenceFileThenEnv(t *testing.T) {
	c := Defaults()
	fsys := fstest.MapFS{"kongctl.yaml": {Data: []byte("manifest:\n  name: from-file\n  kind: ConfigMap\nlint:\n  failSeverity: warn\n")}}
	if err := c.LoadFile(fsys, "kongctl.yaml"); err != nil {
		t.Fatal(err)
	}
	if c.Manifest.Name != "from-file" || c.Manifest.Kind != "ConfigMap" || c.Lint.FailSeverity != "warn" {
		t.Fatalf("file not applied: %+v", c.Manifest)
	}
	// untouched defaults survive a partial file
	if c.Manifest.Key != "kong.yml" || c.Placeholders.Mode != "embedded" {
		t.Fatalf("defaults clobbered: %+v", c)
	}
	err := c.ApplyEnv(lookupFrom(map[string]string{
		"SECRET_NAME":   "from-legacy-env",
		"KONGCTL_ENVS":  "a, b,c",
		"FAKE_SECRETS":  "true",
		"SKIP_LINT":     "true",
		"MAX_LINES":     "12",
		"CI_PROJECT_ID": "42",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Manifest.Name != "from-legacy-env" {
		t.Fatalf("env did not win over file: %s", c.Manifest.Name)
	}
	if strings.Join(c.Envs, ",") != "a,b,c" || !c.FakeSecrets || !c.Lint.Skip || c.Note.MaxLines != 12 || c.GitLab.ProjectID != "42" {
		t.Fatalf("env not applied: %+v", c)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	c := Defaults()
	if err := c.LoadBytes([]byte("manifest:\n  nmae: typo\n"), "x"); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestExternalSecretNeedsStore(t *testing.T) {
	c := Defaults()
	c.Manifest.Kind = "ExternalSecret"
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "storeKind") {
		t.Fatalf("expected store error, got %v", err)
	}
	fsys := fstest.MapFS{"secret-store.yaml": {Data: []byte("secretStoreRef:\n  kind: ClusterSecretStore\n  name: my-store\nrefreshInterval: 2h\nkeyPrefix: PFX_\nkeys:\n  TOKEN: TOKEN\n")}}
	loaded, err := c.LoadStoreFile(fsys)
	if err != nil || !loaded {
		t.Fatalf("store file: %v %v", loaded, err)
	}
	if c.ExternalSec.StoreName != "my-store" || c.ExternalSec.RefreshInterval != "2h" || c.ExternalSec.RemoteKey != "PFX_{name}" || c.ExternalSec.RemoteKeyOverrides["TOKEN"].Key != "TOKEN" {
		t.Fatalf("store file not applied: %+v", c.ExternalSec)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestBadValues(t *testing.T) {
	c := Defaults()
	c.Placeholders.Mode = "sometimes"
	if err := c.Check(); err == nil {
		t.Fatal("bad mode accepted")
	}
	c = Defaults()
	c.Layout.SourceDir = "environments/config"
	if err := c.Check(); err == nil {
		t.Fatal("sourceDir without {env} accepted")
	}
}

func TestResolvedLayoutAndEnvFromPath(t *testing.T) {
	c := Defaults()
	c.Layout.PatchesDir = "environments/{env}/config/kong.patches"
	l := c.ResolvedLayout()
	if env, ok := l.EnvFromPath("environments/dev/config/kong/kong.yml"); !ok || env != "dev" {
		t.Fatalf("EnvFromPath = %q %v", env, ok)
	}
	if env, ok := l.EnvFromPath("environments/sit/config/kong.patches/10.yaml"); !ok || env != "sit" {
		t.Fatalf("patches EnvFromPath = %q %v", env, ok)
	}
	if _, ok := l.EnvFromPath("environments/dev/workloads/kong/values.yaml"); ok {
		t.Fatal("values file must not match")
	}
	if !l.IsRecheckPath("lint/ruleset.yaml") {
		t.Fatal("ruleset must be a recheck path")
	}
}
