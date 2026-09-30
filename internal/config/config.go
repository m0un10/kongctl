// Package config is kongctl's layered configuration:
//
//	defaults  <  kongctl.yaml  <  environment variables  <  flags
//
// Defaults are conventions that make sense for any GitOps repo carrying Kong
// declarative config. Anything organisation-specific (store names, key
// prefixes, annotation domains) has no default and must be set explicitly.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/m0un10/kongctl/internal/layout"
)

// DefaultFileName is the config file looked up at the repository root.
const DefaultFileName = "kongctl.yaml"

// Config is the complete configuration.
type Config struct {
	// Root is the repository root. Empty means "discover" (git toplevel, else cwd).
	Root string `yaml:"root"`
	// Envs is the environment list for multi-env commands. Empty means discover.
	Envs []string `yaml:"envs"`

	Layout       LayoutConfig       `yaml:"layout"`
	Placeholders PlaceholderConfig  `yaml:"placeholders"`
	Validate     ValidateConfig     `yaml:"validate"`
	Lint         LintConfig         `yaml:"lint"`
	Manifest     ManifestConfig     `yaml:"manifest"`
	ExternalSec  ExternalSecretConf `yaml:"externalSecret"`
	Diff         DiffConfig         `yaml:"diff"`
	Note         NoteConfig         `yaml:"note"`
	GitLab       GitLabConfig       `yaml:"gitlab"`
	CMP          CMPConfig          `yaml:"cmp"`
	Log          LogConfig          `yaml:"log"`

	// FakeSecrets fills unset placeholders with fake values. Never set this
	// from an Argo CD Application; the CMP only honours the bare env var.
	FakeSecrets bool `yaml:"fakeSecrets"`
}

// LayoutConfig mirrors layout.Layout in serialisable form.
type LayoutConfig struct {
	SourceDir    string   `yaml:"sourceDir"`
	CommonDir    string   `yaml:"commonDir"`
	PatchesDir   string   `yaml:"patchesDir"`
	Globs        []string `yaml:"globs"`
	EnvNameRegex string   `yaml:"envNameRegex"`
	RecheckPaths []string `yaml:"recheckPaths"`
}

// PlaceholderConfig controls the ${NAME} engine.
type PlaceholderConfig struct {
	// Mode is "embedded" (anywhere in a string) or "whole" (the whole scalar).
	Mode       string `yaml:"mode"`
	Open       string `yaml:"open"`
	Close      string `yaml:"close"`
	NameRegex  string `yaml:"nameRegex"`
	FakePrefix string `yaml:"fakePrefix"`
}

// ValidateConfig controls offline schema validation.
type ValidateConfig struct {
	Skip        bool   `yaml:"skip"`
	KongVersion string `yaml:"kongVersion"`
}

// LintConfig controls the vacuum ruleset run.
type LintConfig struct {
	Skip         bool   `yaml:"skip"`
	Ruleset      string `yaml:"ruleset"`
	FailSeverity string `yaml:"failSeverity"`
	AllowFailure bool   `yaml:"allowFailure"`
}

// ManifestConfig shapes the Kubernetes object that carries the config.
type ManifestConfig struct {
	// Kind is Secret, ConfigMap or ExternalSecret.
	Kind        string            `yaml:"kind"`
	Name        string            `yaml:"name"`
	Key         string            `yaml:"key"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

// RemoteRefOverride customises one placeholder's remoteRef in ExternalSecret mode.
type RemoteRefOverride struct {
	Key      string `yaml:"key"`
	Property string `yaml:"property"`
	Version  string `yaml:"version"`
}

// ExternalSecretConf applies when manifest.kind is ExternalSecret.
type ExternalSecretConf struct {
	APIVersion string `yaml:"apiVersion"`
	// StoreFile is an optional YAML file (secretStoreRef, refreshInterval,
	// keyPrefix, keys) overlaid onto this section when it exists. It is the
	// secret-store.yaml convention of the bash tooling, kept for drop-in use.
	StoreFile       string `yaml:"storeFile"`
	RefreshInterval string `yaml:"refreshInterval"`
	StoreKind       string `yaml:"storeKind"`
	StoreName       string `yaml:"storeName"`
	TargetName      string `yaml:"targetName"`
	CreationPolicy  string `yaml:"creationPolicy"`
	EngineVersion   string `yaml:"engineVersion"`
	// RemoteKey is a template for the store key of a placeholder; {name} is the
	// placeholder name. Default "{name}".
	RemoteKey string `yaml:"remoteKey"`
	// RemoteKeyOverrides maps a placeholder name to an explicit remoteRef.
	RemoteKeyOverrides map[string]RemoteRefOverride `yaml:"remoteKeyOverrides"`
	// Optional remoteRef strategy fields; omitted from the manifest when empty.
	ConversionStrategy string `yaml:"conversionStrategy"`
	DecodingStrategy   string `yaml:"decodingStrategy"`
	MetadataPolicy     string `yaml:"metadataPolicy"`
	// Expression is the template emitted for a whole-value placeholder and
	// EmbeddedExpression for one inside a larger string; {name} is expanded.
	Expression         string `yaml:"expression"`
	EmbeddedExpression string `yaml:"embeddedExpression"`
}

// DiffConfig controls the git comparison.
type DiffConfig struct {
	BaseRef   string `yaml:"baseRef"`
	MergeBase *bool  `yaml:"mergeBase"`
}

// NoteConfig controls the MR note.
type NoteConfig struct {
	Marker   string `yaml:"marker"`
	MaxLines int    `yaml:"maxLines"`
	Provider string `yaml:"provider"`
}

// GitLabConfig is the note provider for GitLab.
type GitLabConfig struct {
	BaseURL   string `yaml:"baseUrl"`
	ProjectID string `yaml:"projectId"`
	MRIID     string `yaml:"mrIid"`
	Token     string `yaml:"token"`
}

// CMPConfig maps Argo CD plugin parameters onto the pipeline.
type CMPConfig struct {
	Prefix         string            `yaml:"prefix"`
	Params         map[string]string `yaml:"params"`
	FakeSecretsEnv string            `yaml:"fakeSecretsEnv"`
}

// LogConfig controls the stderr logger.
type LogConfig struct {
	Level string `yaml:"level"`
}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	t := true
	return &Config{
		Layout: LayoutConfig{
			SourceDir:    "environments/{env}/config/kong",
			Globs:        []string{"*.yml", "*.yaml"},
			EnvNameRegex: "^[a-z0-9-]+$",
			RecheckPaths: []string{DefaultFileName, "lint/ruleset.yaml"},
		},
		Placeholders: PlaceholderConfig{
			Mode:       "embedded",
			Open:       "${",
			Close:      "}",
			NameRegex:  "^[A-Z][A-Z0-9_]*$",
			FakePrefix: "fake-",
		},
		Lint: LintConfig{
			Ruleset:      "lint/ruleset.yaml",
			FailSeverity: "error",
		},
		Manifest: ManifestConfig{
			Kind: "Secret",
			Name: "kong-declarative-config",
			Key:  "kong.yml",
			Labels: map[string]string{
				"app.kubernetes.io/name":       "kong",
				"app.kubernetes.io/component":  "declarative-config",
				"app.kubernetes.io/managed-by": "kongctl",
			},
		},
		ExternalSec: ExternalSecretConf{
			APIVersion:         "external-secrets.io/v1",
			RefreshInterval:    "1h0m0s",
			StoreFile:          "secret-store.yaml",
			ConversionStrategy: "Default",
			DecodingStrategy:   "None",
			MetadataPolicy:     "None",
			CreationPolicy:     "Owner",
			EngineVersion:      "v2",
			RemoteKey:          "{name}",
			Expression:         "{{ .{name} | quote }}",
			EmbeddedExpression: "{{ .{name} }}",
		},
		Diff: DiffConfig{BaseRef: "origin/main", MergeBase: &t},
		Note: NoteConfig{Marker: "<!-- kong-config-diff -->", MaxLines: 400, Provider: "gitlab"},
		CMP: CMPConfig{
			Prefix: "ARGOCD_ENV_",
			Params: map[string]string{
				"env":              "ENV_NAME",
				"basepath":         "BASEPATH",
				"name":             "SECRET_NAME",
				"key":              "SECRET_KEY",
				"kind":             "KIND",
				"skipValidate":     "SKIP_VALIDATE",
				"skipLint":         "SKIP_LINT",
				"allowLintFailure": "ALLOW_LINT_FAILURE",
				"configFile":       "CONFIG_FILE",
			},
			FakeSecretsEnv: "FAKE_SECRETS",
		},
		Log: LogConfig{Level: "info"},
	}
}

// LoadFile overlays a YAML config file. Unknown keys are an error so typos are
// caught. A missing file is reported through fs.ErrNotExist.
func (c *Config) LoadFile(fsys fs.FS, name string) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	return c.LoadBytes(data, name)
}

// LoadBytes overlays YAML config from memory.
func (c *Config) LoadBytes(data []byte, name string) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err.Error() == "EOF" {
			return nil
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Lookup is the environment accessor, injected for tests.
type Lookup func(string) (string, bool)

// ApplyEnv overlays environment variables. Every KONGCTL_* key below is
// accepted, plus the legacy names the bash tooling used.
func (c *Config) ApplyEnv(lookup Lookup) error {
	get := func(names ...string) (string, bool) {
		for _, n := range names {
			if v, ok := lookup(n); ok && v != "" {
				return v, true
			}
		}
		return "", false
	}
	setStr := func(dst *string, names ...string) {
		if v, ok := get(names...); ok {
			*dst = v
		}
	}
	setBool := func(dst *bool, names ...string) error {
		if v, ok := get(names...); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", names[0], err)
			}
			*dst = b
		}
		return nil
	}
	setStr(&c.Root, "KONGCTL_ROOT", "KONG_CONFIG_ROOT")
	if v, ok := get("KONGCTL_ENVS", "ENVS"); ok {
		c.Envs = splitList(v)
	}
	setStr(&c.Layout.SourceDir, "KONGCTL_LAYOUT_SOURCE_DIR")
	setStr(&c.Layout.CommonDir, "KONGCTL_LAYOUT_COMMON_DIR")
	setStr(&c.Layout.PatchesDir, "KONGCTL_LAYOUT_PATCHES_DIR")
	setStr(&c.Placeholders.Mode, "KONGCTL_PLACEHOLDERS_MODE")
	setStr(&c.Placeholders.FakePrefix, "KONGCTL_PLACEHOLDERS_FAKE_PREFIX")
	if err := setBool(&c.Validate.Skip, "KONGCTL_VALIDATE_SKIP", "SKIP_VALIDATE"); err != nil {
		return err
	}
	setStr(&c.Validate.KongVersion, "KONGCTL_VALIDATE_KONG_VERSION")
	if err := setBool(&c.Lint.Skip, "KONGCTL_LINT_SKIP", "SKIP_LINT"); err != nil {
		return err
	}
	setStr(&c.Lint.Ruleset, "KONGCTL_LINT_RULESET", "RULESET")
	setStr(&c.Lint.FailSeverity, "KONGCTL_LINT_FAIL_SEVERITY")
	if err := setBool(&c.Lint.AllowFailure, "KONGCTL_LINT_ALLOW_FAILURE", "ALLOW_LINT_FAILURE"); err != nil {
		return err
	}
	setStr(&c.Manifest.Kind, "KONGCTL_MANIFEST_KIND")
	setStr(&c.Manifest.Name, "KONGCTL_MANIFEST_NAME", "SECRET_NAME")
	setStr(&c.Manifest.Key, "KONGCTL_MANIFEST_KEY", "SECRET_KEY")
	setStr(&c.ExternalSec.StoreKind, "KONGCTL_EXTERNAL_SECRET_STORE_KIND")
	setStr(&c.ExternalSec.StoreName, "KONGCTL_EXTERNAL_SECRET_STORE_NAME")
	setStr(&c.ExternalSec.RemoteKey, "KONGCTL_EXTERNAL_SECRET_REMOTE_KEY")
	setStr(&c.ExternalSec.RefreshInterval, "KONGCTL_EXTERNAL_SECRET_REFRESH_INTERVAL")
	setStr(&c.Diff.BaseRef, "KONGCTL_DIFF_BASE_REF", "BASE_REF")
	if v, ok := get("KONGCTL_NOTE_MAX_LINES", "MAX_LINES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("MAX_LINES: %w", err)
		}
		c.Note.MaxLines = n
	}
	setStr(&c.Note.Marker, "KONGCTL_NOTE_MARKER")
	setStr(&c.Note.Provider, "KONGCTL_NOTE_PROVIDER")
	setStr(&c.GitLab.BaseURL, "KONGCTL_GITLAB_BASE_URL", "CI_API_V4_URL")
	setStr(&c.GitLab.ProjectID, "KONGCTL_GITLAB_PROJECT_ID", "CI_PROJECT_ID")
	setStr(&c.GitLab.MRIID, "KONGCTL_GITLAB_MR_IID", "CI_MERGE_REQUEST_IID")
	setStr(&c.GitLab.Token, "KONGCTL_GITLAB_TOKEN", "GITLAB_TOKEN")
	setStr(&c.CMP.Prefix, "KONGCTL_CMP_PREFIX")
	setStr(&c.CMP.FakeSecretsEnv, "KONGCTL_CMP_FAKE_SECRETS_ENV")
	setStr(&c.Log.Level, "KONGCTL_LOG_LEVEL")
	// The bare FAKE_SECRETS is the only spelling the CMP accepts; keep it so the
	// same env works locally.
	if err := setBool(&c.FakeSecrets, "KONGCTL_FAKE_SECRETS", c.CMP.FakeSecretsEnv); err != nil {
		return err
	}
	return nil
}

// Check validates values that must be well formed regardless of command.
func (c *Config) Check() error {
	if _, err := regexp.Compile(c.Layout.EnvNameRegex); err != nil {
		return fmt.Errorf("layout.envNameRegex: %w", err)
	}
	if !strings.Contains(c.Layout.SourceDir, "{env}") {
		return fmt.Errorf("layout.sourceDir %q must contain {env}", c.Layout.SourceDir)
	}
	if _, err := regexp.Compile(c.Placeholders.NameRegex); err != nil {
		return fmt.Errorf("placeholders.nameRegex: %w", err)
	}
	switch c.Placeholders.Mode {
	case "embedded", "whole":
	default:
		return fmt.Errorf("placeholders.mode must be embedded or whole, got %q", c.Placeholders.Mode)
	}
	if c.Placeholders.Open == "" || c.Placeholders.Close == "" {
		return errors.New("placeholders.open and placeholders.close must be set")
	}
	switch strings.ToLower(c.Lint.FailSeverity) {
	case "hint", "info", "warn", "error":
	default:
		return fmt.Errorf("lint.failSeverity must be hint, info, warn or error, got %q", c.Lint.FailSeverity)
	}
	switch c.Manifest.Kind {
	case "Secret", "ConfigMap":
	case "ExternalSecret":
		if c.ExternalSec.StoreKind == "" || c.ExternalSec.StoreName == "" {
			return errors.New("manifest.kind ExternalSecret needs externalSecret.storeKind and externalSecret.storeName (no default: they name your organisation's secret store)")
		}
	default:
		return fmt.Errorf("manifest.kind must be Secret, ConfigMap or ExternalSecret, got %q", c.Manifest.Kind)
	}
	if c.Manifest.Name == "" || c.Manifest.Key == "" {
		return errors.New("manifest.name and manifest.key must be set")
	}
	if c.Note.MaxLines <= 0 {
		return errors.New("note.maxLines must be positive")
	}
	return nil
}

// ResolvedLayout builds the resolved layout. Call Check first.
func (c *Config) ResolvedLayout() layout.Layout {
	re := regexp.MustCompile(c.Layout.EnvNameRegex)
	return layout.Layout{
		SourceDir:    strings.Trim(c.Layout.SourceDir, "/"),
		CommonDir:    strings.Trim(c.Layout.CommonDir, "/"),
		PatchesDir:   strings.Trim(c.Layout.PatchesDir, "/"),
		Globs:        c.Layout.Globs,
		EnvNameRegex: re,
		RecheckPaths: c.Layout.RecheckPaths,
	}
}

// UseMergeBase reports whether diff compares against merge-base(base, HEAD).
func (c *Config) UseMergeBase() bool {
	return c.Diff.MergeBase == nil || *c.Diff.MergeBase
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SplitList exposes the env-list parser for flags.
func SplitList(s string) []string { return splitList(s) }
