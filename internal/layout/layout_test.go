package layout

import (
	"reflect"
	"regexp"
	"testing"
	"testing/fstest"
)

func TestDiscoverAndList(t *testing.T) {
	l := Layout{
		SourceDir:    "environments/{env}/config/kong",
		Globs:        []string{"*.yml", "*.yaml"},
		EnvNameRegex: regexp.MustCompile(`^[a-z0-9-]+$`),
	}
	fsys := fstest.MapFS{
		"environments/prod/config/kong/kong.yml":        {Data: []byte("")},
		"environments/dev/config/kong/b.yaml":           {Data: []byte("")},
		"environments/dev/config/kong/a.yml":            {Data: []byte("")},
		"environments/dev/config/kong/nested/skip.yaml": {Data: []byte("")},
		"environments/dev/config/kong/README.md":        {Data: []byte("")},
		"environments/o11y/values.yaml":                 {Data: []byte("")},
		"environments/Bad_Name/config/kong/kong.yml":    {Data: []byte("")},
	}
	envs, err := l.DiscoverEnvs(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envs, []string{"dev", "prod"}) {
		t.Fatalf("envs = %v", envs)
	}
	files, err := l.Sources(fsys, "dev")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"environments/dev/config/kong/a.yml", "environments/dev/config/kong/b.yaml"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v", files)
	}
	if err := l.ValidateEnv("../x"); err == nil {
		t.Fatal("traversal accepted")
	}
}
