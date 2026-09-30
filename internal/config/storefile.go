package config

import (
	"errors"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// storeFile is the secret-store.yaml shape used by the bash tooling.
type storeFile struct {
	SecretStoreRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"secretStoreRef"`
	RefreshInterval string            `yaml:"refreshInterval"`
	KeyPrefix       string            `yaml:"keyPrefix"`
	Keys            map[string]string `yaml:"keys"`
}

// LoadStoreFile overlays externalSecret settings from the configured store
// file when it exists. It reports whether a file was read.
func (c *Config) LoadStoreFile(fsys fs.FS) (bool, error) {
	name := c.ExternalSec.StoreFile
	if name == "" {
		return false, nil
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var sf storeFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	if sf.SecretStoreRef.Kind != "" {
		c.ExternalSec.StoreKind = sf.SecretStoreRef.Kind
	}
	if sf.SecretStoreRef.Name != "" {
		c.ExternalSec.StoreName = sf.SecretStoreRef.Name
	}
	if sf.RefreshInterval != "" {
		c.ExternalSec.RefreshInterval = sf.RefreshInterval
	}
	if sf.KeyPrefix != "" {
		c.ExternalSec.RemoteKey = sf.KeyPrefix + "{name}"
	}
	if len(sf.Keys) > 0 {
		if c.ExternalSec.RemoteKeyOverrides == nil {
			c.ExternalSec.RemoteKeyOverrides = map[string]RemoteRefOverride{}
		}
		for n, k := range sf.Keys {
			c.ExternalSec.RemoteKeyOverrides[n] = RemoteRefOverride{Key: k}
		}
	}
	return true, nil
}
