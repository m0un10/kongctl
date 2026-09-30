package hook

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/m0un10/kongctl/internal/gitx"
	"github.com/m0un10/kongctl/internal/layout"
)

func testLayout() layout.Layout {
	return layout.Layout{
		SourceDir:    "environments/{env}/config/kong",
		CommonDir:    "environments/_common/config/kong",
		PatchesDir:   "environments/{env}/config/kong.patches",
		EnvNameRegex: regexp.MustCompile(`^[a-z0-9-]+$`),
		RecheckPaths: []string{"kongctl.yaml", "lint/ruleset.yaml"},
	}
}

func TestStagedPlan(t *testing.T) {
	g := gitx.NewFake()
	g.Staged = []string{"environments/sit/config/kong/kong.yml", "environments/dev/config/kong.patches/1.yaml", "README.md", "environments/dev/config/kong/x.yaml"}
	plan, err := StagedPlan(context.Background(), g, testLayout())
	if err != nil {
		t.Fatal(err)
	}
	if plan.All || !reflect.DeepEqual(plan.Envs, []string{"dev", "sit"}) {
		t.Fatalf("plan = %+v", plan)
	}
	g.Staged = []string{"lint/ruleset.yaml"}
	plan, _ = StagedPlan(context.Background(), g, testLayout())
	if !plan.All {
		t.Fatal("ruleset change must recheck all")
	}
	g.Staged = []string{"environments/_common/config/kong/g.yaml"}
	plan, _ = StagedPlan(context.Background(), g, testLayout())
	if !plan.All {
		t.Fatal("common change must recheck all")
	}
	g.Staged = []string{"docs/x.md"}
	plan, _ = StagedPlan(context.Background(), g, testLayout())
	if plan.All || len(plan.Envs) != 0 {
		t.Fatal("unrelated change must be a no-op")
	}
}

func TestInstall(t *testing.T) {
	g := gitx.NewFake()
	g.Top = t.TempDir()
	written, err := Install(context.Background(), g, ".githooks")
	if err != nil || !written {
		t.Fatalf("install: %v %v", written, err)
	}
	data, err := os.ReadFile(filepath.Join(g.Top, ".githooks", "pre-commit"))
	if err != nil || string(data) != Script {
		t.Fatalf("hook content: %v %q", err, data)
	}
	if g.Configs["core.hooksPath"] != ".githooks" {
		t.Fatal("core.hooksPath not set")
	}
	written, _ = Install(context.Background(), g, ".githooks")
	if written {
		t.Fatal("existing hook must be left alone")
	}
}
