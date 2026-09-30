// Package manifest wraps a Kong declarative config in the Kubernetes object
// that delivers it to the gateway: a Secret, a ConfigMap, or an External
// Secrets Operator ExternalSecret whose template carries the config.
package manifest

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/m0un10/kongctl/internal/yamlx"
)

// Kinds.
const (
	KindSecret         = "Secret"
	KindConfigMap      = "ConfigMap"
	KindExternalSecret = "ExternalSecret"
)

// Spec is what every kind shares.
type Spec struct {
	Kind        string
	Name        string
	Key         string
	Labels      map[string]string
	Annotations map[string]string
	// Env is expanded into label and annotation values as {env}.
	Env string
	// Data is the (rendered or templated) config.
	Data []byte
}

func expandMap(m map[string]string, env string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = strings.ReplaceAll(v, "{env}", env)
	}
	return out
}

func metadata(spec Spec) *yaml.Node {
	md := yamlx.Mapping(yamlx.Scalar("name", 0), yamlx.Scalar(spec.Name, 0))
	if labels := expandMap(spec.Labels, spec.Env); len(labels) > 0 {
		md.Content = append(md.Content, yamlx.Scalar("labels", 0), yamlx.StringMap(labels))
	}
	if ann := expandMap(spec.Annotations, spec.Env); len(ann) > 0 {
		md.Content = append(md.Content, yamlx.Scalar("annotations", 0), yamlx.StringMap(ann))
	}
	return md
}

// Build renders a Secret or ConfigMap. Secrets carry the config under
// `data`, base64 encoded, so Argo CD diffs need no normalisation; ConfigMaps
// carry it as a literal block.
func Build(spec Spec) ([]byte, error) {
	if spec.Name == "" || spec.Key == "" {
		return nil, errors.New("manifest name and key are required")
	}
	root := yamlx.Mapping(
		yamlx.Scalar("apiVersion", 0), yamlx.Scalar("v1", 0),
		yamlx.Scalar("kind", 0), yamlx.Scalar(spec.Kind, 0),
		yamlx.Scalar("metadata", 0), metadata(spec),
	)
	switch spec.Kind {
	case KindSecret:
		root.Content = append(root.Content,
			yamlx.Scalar("type", 0), yamlx.Scalar("Opaque", 0),
			yamlx.Scalar("data", 0), yamlx.Mapping(
				yamlx.Scalar(spec.Key, 0),
				yamlx.Scalar(base64.StdEncoding.EncodeToString(spec.Data), 0),
			),
		)
	case KindConfigMap:
		root.Content = append(root.Content,
			yamlx.Scalar("data", 0), yamlx.Mapping(
				yamlx.Scalar(spec.Key, 0),
				yamlx.Scalar(string(spec.Data), yaml.LiteralStyle),
			),
		)
	default:
		return nil, fmt.Errorf("unsupported manifest kind %q", spec.Kind)
	}
	return yamlx.Encode(root)
}

// RemoteRef is one ExternalSecret data entry's remoteRef.
type RemoteRef struct {
	Key                string
	Property           string
	Version            string
	ConversionStrategy string
	DecodingStrategy   string
	MetadataPolicy     string
}

// ExternalSecretSpec is the ESO-specific part.
type ExternalSecretSpec struct {
	APIVersion      string
	RefreshInterval string
	StoreKind       string
	StoreName       string
	TargetName      string
	CreationPolicy  string
	EngineVersion   string
	// Entries maps each placeholder name (the secretKey) to its remoteRef.
	Entries map[string]RemoteRef
}

// BuildExternalSecret renders an ExternalSecret whose target template holds
// spec.Data (the config with template expressions) and whose data list pulls
// every placeholder from the store.
func BuildExternalSecret(spec Spec, es ExternalSecretSpec) ([]byte, error) {
	if spec.Name == "" || spec.Key == "" {
		return nil, errors.New("manifest name and key are required")
	}
	if es.StoreKind == "" || es.StoreName == "" {
		return nil, errors.New("externalSecret.storeKind and externalSecret.storeName are required")
	}
	target := spec.Name
	if es.TargetName != "" {
		target = es.TargetName
	}
	template := yamlx.Mapping(
		yamlx.Scalar("engineVersion", 0), yamlx.Scalar(es.EngineVersion, 0),
		yamlx.Scalar("type", 0), yamlx.Scalar("Opaque", 0),
		yamlx.Scalar("data", 0), yamlx.Mapping(
			yamlx.Scalar(spec.Key, 0),
			yamlx.Scalar(string(spec.Data), yaml.LiteralStyle),
		),
	)
	targetNode := yamlx.Mapping(
		yamlx.Scalar("name", 0), yamlx.Scalar(target, 0),
		yamlx.Scalar("creationPolicy", 0), yamlx.Scalar(es.CreationPolicy, 0),
		yamlx.Scalar("template", 0), template,
	)
	names := make([]string, 0, len(es.Entries))
	for n := range es.Entries {
		names = append(names, n)
	}
	sort.Strings(names)
	data := yamlx.Sequence()
	for _, n := range names {
		ref := es.Entries[n]
		refNode := yamlx.Mapping(yamlx.Scalar("key", 0), yamlx.Scalar(ref.Key, 0))
		add := func(k, v string) {
			if v != "" {
				refNode.Content = append(refNode.Content, yamlx.Scalar(k, 0), yamlx.Scalar(v, 0))
			}
		}
		add("property", ref.Property)
		add("version", ref.Version)
		add("conversionStrategy", ref.ConversionStrategy)
		add("decodingStrategy", ref.DecodingStrategy)
		add("metadataPolicy", ref.MetadataPolicy)
		data.Content = append(data.Content, yamlx.Mapping(
			yamlx.Scalar("secretKey", 0), yamlx.Scalar(n, 0),
			yamlx.Scalar("remoteRef", 0), refNode,
		))
	}
	specNode := yamlx.Mapping(
		yamlx.Scalar("refreshInterval", 0), yamlx.Scalar(es.RefreshInterval, 0),
		yamlx.Scalar("secretStoreRef", 0), yamlx.Mapping(
			yamlx.Scalar("kind", 0), yamlx.Scalar(es.StoreKind, 0),
			yamlx.Scalar("name", 0), yamlx.Scalar(es.StoreName, 0),
		),
		yamlx.Scalar("target", 0), targetNode,
	)
	if len(names) > 0 {
		specNode.Content = append(specNode.Content, yamlx.Scalar("data", 0), data)
	}
	root := yamlx.Mapping(
		yamlx.Scalar("apiVersion", 0), yamlx.Scalar(es.APIVersion, 0),
		yamlx.Scalar("kind", 0), yamlx.Scalar(KindExternalSecret, 0),
		yamlx.Scalar("metadata", 0), metadata(spec),
		yamlx.Scalar("spec", 0), specNode,
	)
	return yamlx.Encode(root)
}
