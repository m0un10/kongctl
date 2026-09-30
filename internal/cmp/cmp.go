// Package cmp adapts kongctl to the Argo CD Config Management Plugin
// contract. Argo CD prefixes every spec.source.plugin.env entry with
// ARGOCD_ENV_; this package maps those onto the pipeline configuration.
//
// Two rules are deliberate. Secret values are read from plain environment
// variables (the sidecar's envFrom), never through the prefixed parameters,
// so an Application cannot inject them. And fake secrets can only be enabled
// by the bare FAKE_SECRETS variable on the container, never by an Application.
package cmp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/m0un10/kongctl/internal/config"
	"github.com/m0un10/kongctl/internal/kerr"
	"github.com/m0un10/kongctl/internal/logx"
)

// Options are the resolved plugin inputs.
type Options struct {
	Env        string
	RepoRoot   string
	ConfigFile string
	// FakeSecrets came from the bare container env var.
	FakeSecrets bool
	// IgnoredFakeParam is set when a prefixed FAKE_SECRETS was seen and ignored.
	IgnoredFakeParam bool
	// DeprecatedConfigScript is set when the old CONFIG_SCRIPT parameter was seen.
	DeprecatedConfigScript bool
}

// FromEnv reads plugin parameters and applies them to cfg. cwd is the
// directory Argo CD runs the plugin in (the Application's path).
func FromEnv(lookup config.Lookup, cfg *config.Config, cwd string) (Options, error) {
	prefix := cfg.CMP.Prefix
	param := func(key string) (string, bool) {
		name, ok := cfg.CMP.Params[key]
		if !ok || name == "" {
			return "", false
		}
		v, ok := lookup(prefix + name)
		return v, ok && v != ""
	}
	boolParam := func(key string, dst *bool) error {
		v, ok := param(key)
		if !ok {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return kerr.Usagef("%s%s: %w", prefix, cfg.CMP.Params[key], err)
		}
		*dst = b
		return nil
	}

	var o Options
	env, ok := param("env")
	if !ok {
		return o, kerr.Usagef("%s%s not set (application_environment label)", prefix, cfg.CMP.Params["env"])
	}
	o.Env = env

	basepath, _ := param("basepath")
	basepath = strings.Trim(basepath, "/")
	root := cwd
	if basepath != "" {
		root = filepath.Join(cwd, filepath.FromSlash(basepath))
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return o, kerr.Usagef("basepath %q does not exist under %s", basepath, cwd)
	}
	o.RepoRoot = root
	cfg.Root = root

	if v, ok := param("name"); ok {
		cfg.Manifest.Name = v
	}
	if v, ok := param("key"); ok {
		cfg.Manifest.Key = v
	}
	if v, ok := param("kind"); ok {
		cfg.Manifest.Kind = v
	}
	if err := boolParam("skipValidate", &cfg.Validate.Skip); err != nil {
		return o, err
	}
	if err := boolParam("skipLint", &cfg.Lint.Skip); err != nil {
		return o, err
	}
	if err := boolParam("allowLintFailure", &cfg.Lint.AllowFailure); err != nil {
		return o, err
	}
	if v, ok := param("configFile"); ok {
		o.ConfigFile = v
	}
	if _, ok := lookup(prefix + "CONFIG_SCRIPT"); ok {
		o.DeprecatedConfigScript = true
	}
	if _, ok := lookup(prefix + cfg.CMP.FakeSecretsEnv); ok {
		o.IgnoredFakeParam = true
	}
	if v, ok := lookup(cfg.CMP.FakeSecretsEnv); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return o, kerr.Usagef("%s: %w", cfg.CMP.FakeSecretsEnv, err)
		}
		o.FakeSecrets = b
	}
	return o, nil
}

// LogWarnings emits the advisory messages about ignored inputs.
func (o Options) LogWarnings(log *logx.Logger, cfg *config.Config) {
	if o.IgnoredFakeParam {
		log.Warn(fmt.Sprintf("%s%s is ignored: fake secrets can only be enabled by the bare %s on the container", cfg.CMP.Prefix, cfg.CMP.FakeSecretsEnv, cfg.CMP.FakeSecretsEnv))
	}
	if o.DeprecatedConfigScript {
		log.Warn(fmt.Sprintf("%sCONFIG_SCRIPT is no longer used; kongctl composes in-process", cfg.CMP.Prefix))
	}
	if o.FakeSecrets {
		log.Warn("FAKE_SECRETS=true: placeholders will be filled with fake values (local testing only)")
	}
}

// ErrNoSourceDir is returned when the environment has no source directory.
var ErrNoSourceDir = errors.New("no source directory")
