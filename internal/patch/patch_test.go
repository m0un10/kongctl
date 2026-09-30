package patch

import (
	"testing"

	"github.com/m0un10/kongctl/internal/yamlx"
)

const base = `_format_version: "3.0"
plugins:
- name: opentelemetry
  protocols: [http, https]
  config:
    traces_endpoint: http://collector:4318/v1/traces
    resource_attributes:
      service.name: kong
      deployment.environment.name: REPLACE_ME
`

func apply(t *testing.T, patchYAML string) map[string]any {
	t.Helper()
	data, err := yamlx.Deserialize([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Apply(data, []Source{{Name: "p.yaml", Data: []byte(patchYAML)}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func cfg(m map[string]any) map[string]any {
	return m["plugins"].([]any)[0].(map[string]any)["config"].(map[string]any)
}

func TestTopLevelKeyReplacesWholesale(t *testing.T) {
	out := apply(t, `patches:
- selectors: ["$.plugins[?(@.name=='opentelemetry')]"]
  values:
    config:
      resource_attributes:
        deployment.environment.name: t1
`)
	c := cfg(out)
	if _, ok := c["traces_endpoint"]; ok {
		t.Fatal("expected config to be replaced wholesale (decK semantics), traces_endpoint survived")
	}
}

func TestSelectorAtNestedMapKeepsSiblings(t *testing.T) {
	out := apply(t, `patches:
- selectors: ["$.plugins[?(@.name=='opentelemetry')].config.resource_attributes"]
  values:
    deployment.environment.name: t1
`)
	c := cfg(out)
	if c["traces_endpoint"] != "http://collector:4318/v1/traces" {
		t.Fatal("sibling key lost")
	}
	ra := c["resource_attributes"].(map[string]any)
	if ra["deployment.environment.name"] != "t1" || ra["service.name"] != "kong" {
		t.Fatalf("nested patch wrong: %v", ra)
	}
}

func TestArraysReplaceAndRemove(t *testing.T) {
	out := apply(t, `patches:
- selectors: ["$.plugins[?(@.name=='opentelemetry')]"]
  values:
    protocols: [grpc]
  remove: [config]
`)
	p := out["plugins"].([]any)[0].(map[string]any)
	if got := p["protocols"].([]any); len(got) != 1 || got[0] != "grpc" {
		t.Fatalf("protocols = %v, want [grpc]", got)
	}
	if _, ok := p["config"]; ok {
		t.Fatal("remove did not delete config")
	}
}

func TestBadPatchFile(t *testing.T) {
	data, _ := yamlx.Deserialize([]byte(base))
	if _, err := Apply(data, []Source{{Name: "bad.yaml", Data: []byte("patches: notalist\n")}}); err == nil {
		t.Fatal("expected error")
	}
}
