package note

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/m0un10/kongctl/internal/review"
)

func opts() Options {
	return Options{Marker: "<!-- m -->", MaxLines: 3, BaseRef: "origin/main (abc1234)", HeadSHA: "def5678", RunHint: "kongctl diff"}
}

func TestNoChanges(t *testing.T) {
	body := Build([]review.Result{{Env: "dev", Status: review.StatusUnchanged, Identical: true}}, opts())
	if !strings.HasPrefix(body, "<!-- m -->\n### Kong config diff\n\n**No changes.**") {
		t.Fatalf("unexpected body:\n%s", body)
	}
}

func TestLayoutRules(t *testing.T) {
	results := []review.Result{
		{Env: "a", Status: review.StatusUnchanged, Identical: true},
		{Env: "b", Status: review.StatusUnchanged, Identical: false, Raw: "--- a\n+++ b\n-x\n+y\n"},
		{Env: "c", Status: review.StatusChanged, Identical: false, Semantic: "l1\nl2\nl3\nl4\nl5\n", Raw: "r\n"},
		{Env: "d", Status: review.StatusError, Err: errors.New("boom")},
		{Env: "e", Status: review.StatusAbsent, Identical: true},
	}
	body := Build(results, opts())
	for _, want := range []string{
		"| `a` | ✅ none | ✅ none |",
		"| `b` | ✅ none | 🎨 formatting only |",
		"| `c` | 🔴 changed | 🎨 changed |",
		"| `d` | ⚠️ error | — |",
		"<details open>\n<summary>🎨 <code>b</code> — formatting only, no semantic change</summary>\n\n```diff\n",
		"<details open>\n<summary>🔴 <code>c</code> — semantic changes</summary>",
		"<details>\n<summary><code>c</code> — formatting / raw diff</summary>",
		"... truncated: 3 of 5 lines shown. Run `kongctl diff` locally for the full diff.",
		"boom",
		"<sub>Base `origin/main (abc1234)` · head `def5678`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "`e`") {
		t.Fatal("absent env must not appear")
	}
}

type fakeProvider struct {
	findErr error
	found   string
	actions []string
}

func (f *fakeProvider) FindNote(context.Context, string) (string, bool, error) {
	return f.found, f.found != "", f.findErr
}
func (f *fakeProvider) CreateNote(context.Context, string) error {
	f.actions = append(f.actions, "create")
	return nil
}
func (f *fakeProvider) UpdateNote(_ context.Context, id, _ string) error {
	f.actions = append(f.actions, "update "+id)
	return nil
}

func TestPost(t *testing.T) {
	p := &fakeProvider{found: "7"}
	if action, _, err := Post(context.Background(), p, "m", "b"); err != nil || action != "updated 7" {
		t.Fatalf("update: %v %v", action, err)
	}
	p = &fakeProvider{}
	if action, _, err := Post(context.Background(), p, "m", "b"); err != nil || action != "created" {
		t.Fatalf("create: %v %v", action, err)
	}
	p = &fakeProvider{findErr: errors.New("401")}
	action, lookupErr, err := Post(context.Background(), p, "m", "b")
	if err != nil || action != "created" || lookupErr == nil {
		t.Fatalf("lookup failure must degrade to create with a warning: %v %v %v", action, lookupErr, err)
	}
}
