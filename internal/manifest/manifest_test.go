package manifest

import (
	"encoding/base64"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSecret(t *testing.T) {
	out, err := Build(Spec{
		Kind: KindSecret, Name: "kong-declarative-config", Key: "kong.yml", Env: "dev",
		Labels:      map[string]string{"app.kubernetes.io/name": "kong"},
		Annotations: map[string]string{"example.com/environment": "{env}"},
		Data:        []byte("_format_version: \"3.0\"\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: kong-declarative-config\n  labels:\n    app.kubernetes.io/name: kong\n  annotations:\n    example.com/environment: dev\ntype: Opaque\ndata:\n  kong.yml: " +
		base64.StdEncoding.EncodeToString([]byte("_format_version: \"3.0\"\n")) + "\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestConfigMapAndBadKind(t *testing.T) {
	out, err := Build(Spec{Kind: KindConfigMap, Name: "n", Key: "k", Data: []byte("a: 1\nb: 2\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "data:\n  k: |\n    a: 1\n    b: 2\n") {
		t.Fatalf("configmap block wrong:\n%s", out)
	}
	if _, err := Build(Spec{Kind: "Weird", Name: "n", Key: "k"}); err == nil {
		t.Fatal("bad kind accepted")
	}
}

func TestExternalSecret(t *testing.T) {
	out, err := BuildExternalSecret(Spec{Kind: KindExternalSecret, Name: "kong-declarative-config", Key: "kong.yml", Data: []byte("key: {{ .A | quote }}\n")},
		ExternalSecretSpec{
			APIVersion: "external-secrets.io/v1", RefreshInterval: "1h0m0s", StoreKind: "ClusterSecretStore", StoreName: "store",
			CreationPolicy: "Owner", EngineVersion: "v2",
			Entries: map[string]RemoteRef{
				"B": {Key: "PFX_B", ConversionStrategy: "Default"},
				"A": {Key: "A", Property: "p"},
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not valid yaml: %v\n%s", err, out)
	}
	spec := doc["spec"].(map[string]any)
	data := spec["data"].([]any)
	if len(data) != 2 || data[0].(map[string]any)["secretKey"] != "A" {
		t.Fatalf("data entries wrong or unsorted: %v", data)
	}
	tmpl := spec["target"].(map[string]any)["template"].(map[string]any)
	if tmpl["type"] != "Opaque" || tmpl["engineVersion"] != "v2" {
		t.Fatalf("template header wrong: %v", tmpl)
	}
	if tmpl["data"].(map[string]any)["kong.yml"] != "key: {{ .A | quote }}\n" {
		t.Fatalf("template body wrong: %q", tmpl["data"])
	}
	if !strings.Contains(string(out), "property: p") || strings.Contains(string(out), "metadataPolicy") {
		t.Fatalf("optional remoteRef fields wrong:\n%s", out)
	}
	if _, err := BuildExternalSecret(Spec{Name: "n", Key: "k"}, ExternalSecretSpec{}); err == nil {
		t.Fatal("missing store accepted")
	}
}
