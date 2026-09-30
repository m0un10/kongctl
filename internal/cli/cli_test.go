package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/m0un10/kongctl/internal/kerr"
)

func runCLI(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := NewApp()
	a.Stdout, a.Stderr = &out, &errb
	a.Lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	a.Getwd = func() (string, error) { return filepath.Abs("../../testdata/repo") }
	cmd := a.Command()
	cmd.SetArgs(args)
	cmd.SetOut(&errb)
	cmd.SetErr(&errb)
	err := cmd.ExecuteContext(context.Background())
	if err != nil {
		errb.WriteString("[kongctl] ERROR: " + err.Error() + "\n")
	}
	return out.String(), errb.String(), kerr.ExitCode(err)
}

func TestComposeStdoutIsArtifactOnly(t *testing.T) {
	out, errs, code := runCLI(t, nil, "compose", "dev", "--root", "../../testdata/repo")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errs)
	}
	if !strings.HasPrefix(out, "_format_version: \"3.0\"\nconsumers:\n") || strings.Contains(out, "[kongctl]") {
		t.Fatalf("stdout polluted or wrong:\n%s", out)
	}
	if !strings.Contains(errs, "compose dev: 3 file(s)") {
		t.Fatalf("log missing on stderr: %s", errs)
	}
}

func TestPlaceholdersAndRenderExitCodes(t *testing.T) {
	out, _, code := runCLI(t, nil, "placeholders", "dev", "--root", "../../testdata/repo", "-q")
	if code != 0 || out != "CONSUMER_ALPHA_BASICAUTH_PASSWORD\nCONSUMER_ALPHA_KEYAUTH_KEY\nCONSUMER_BETA_KEYAUTH_KEY\n" {
		t.Fatalf("placeholders: %d %q", code, out)
	}
	_, errs, code := runCLI(t, nil, "render", "dev", "--root", "../../testdata/repo")
	if code != 1 || !strings.Contains(errs, "unset or empty placeholders") {
		t.Fatalf("render without values: %d %s", code, errs)
	}
	out, _, code = runCLI(t, map[string]string{"CONSUMER_ALPHA_BASICAUTH_PASSWORD": "p", "CONSUMER_ALPHA_KEYAUTH_KEY": "k", "CONSUMER_BETA_KEYAUTH_KEY": "b"}, "render", "dev", "--root", "../../testdata/repo", "-q")
	if code != 0 || !strings.Contains(out, "password: p\n") {
		t.Fatalf("render with values: %d %s", code, out)
	}
	_, _, code = runCLI(t, nil, "render", "Dev;x", "--root", "../../testdata/repo")
	if code != 2 {
		t.Fatalf("unsafe env name should be usage error, got %d", code)
	}
}

func TestCheckAndManifest(t *testing.T) {
	_, errs, code := runCLI(t, nil, "check", "--root", "../../testdata/repo", "-e", "dev,prod")
	if code != 0 || !strings.Contains(errs, "==> OK (2 environment(s) checked)") {
		t.Fatalf("check: %d %s", code, errs)
	}
	_, errs, code = runCLI(t, nil, "check", "--root", "../../testdata/repo")
	if code != 1 || !strings.Contains(errs, "FAIL:  broken") || !strings.Contains(errs, "==> FAILED (3 environment(s) checked)") {
		t.Fatalf("check with broken: %d %s", code, errs)
	}
	out, _, code := runCLI(t, nil, "manifest", "prod", "--root", "../../testdata/repo", "--kind", "ExternalSecret", "-q")
	if code != 0 || !strings.Contains(out, "kind: ExternalSecret") || !strings.Contains(out, "key: MY_TOKEN") {
		t.Fatalf("manifest ExternalSecret: %d %s", code, out)
	}
	_, errs, code = runCLI(t, nil, "manifest", "prod", "--root", "../../testdata/repo", "--kind", "ExternalSecret", "--config", "../../internal/cli/testdata/nostore.yaml")
	if code != 2 || !strings.Contains(errs, "storeKind") {
		t.Fatalf("ExternalSecret without store must be a usage error: %d %s", code, errs)
	}
}

func TestCmpGenerate(t *testing.T) {
	env := map[string]string{
		"ARGOCD_ENV_ENV_NAME":     "prod",
		"ARGOCD_ENV_KIND":         "ExternalSecret",
		"ARGOCD_ENV_FAKE_SECRETS": "true",
		"ARGOCD_APP_NAME":         "example-prod",
	}
	out, errs, code := runCLI(t, env, "cmp", "generate")
	if code != 0 {
		t.Fatalf("cmp generate: %d %s", code, errs)
	}
	if strings.Count(out, "\nkind: ") != 1 || !strings.HasPrefix(out, "apiVersion: external-secrets.io/v1\nkind: ExternalSecret\n") {
		t.Fatalf("stdout must be exactly one manifest:\n%s", out)
	}
	if !strings.Contains(errs, "ARGOCD_ENV_FAKE_SECRETS is ignored") {
		t.Fatalf("expected ignored-parameter warning: %s", errs)
	}
	// Secret kind without values fails and leaves stdout empty.
	env["ARGOCD_ENV_KIND"] = "Secret"
	out, errs, code = runCLI(t, env, "cmp", "generate")
	if code != 1 || out != "" || !strings.Contains(errs, "unset or empty placeholders") {
		t.Fatalf("cmp generate Secret without values: %d out=%q errs=%s", code, out, errs)
	}
	_, errs, code = runCLI(t, map[string]string{}, "cmp", "init")
	if code != 2 || !strings.Contains(errs, "ENV_NAME") {
		t.Fatalf("cmp init without env: %d %s", code, errs)
	}
}
