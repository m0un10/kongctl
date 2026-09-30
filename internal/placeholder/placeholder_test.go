package placeholder

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func whole() Syntax {
	s := DefaultSyntax()
	s.Mode = ModeWhole
	return s
}

func TestScanEmbedded(t *testing.T) {
	doc := map[string]any{
		"consumers": []any{
			map[string]any{"key": "${A_KEY}", "host": "svc-${HOST}.internal", "regex": "~/x(?<y>/.*)?$", "uri": "/amm$(uri_captures[1])"},
		},
		"escaped": "literal $${NOT_A_PLACEHOLDER}",
	}
	rep := Scan(doc, DefaultSyntax())
	if got, want := rep.Names, []string{"A_KEY", "HOST"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if len(rep.Offenders) != 0 {
		t.Fatalf("unexpected offenders: %+v", rep.Offenders)
	}
	var wholeCount int
	for _, o := range rep.Occurrences {
		if o.Whole {
			wholeCount++
		}
	}
	if wholeCount != 1 {
		t.Fatalf("expected exactly one whole-value occurrence, got %d", wholeCount)
	}
}

func TestScanOffenders(t *testing.T) {
	cases := map[string]any{
		"lowercase": map[string]any{"k": "${host}"},
		"spaces":    map[string]any{"k": "${ HOST }"},
		"unclosed":  map[string]any{"k": "${HOST"},
		"key":       map[string]any{"${NAME}": "v"},
	}
	for name, doc := range cases {
		rep := Scan(doc, DefaultSyntax())
		if len(rep.Offenders) != 1 {
			t.Errorf("%s: offenders = %+v, want exactly one", name, rep.Offenders)
		}
	}
	// whole mode rejects embedded
	rep := Scan(map[string]any{"k": "svc-${HOST}"}, whole())
	if len(rep.Offenders) != 1 {
		t.Fatalf("whole mode should reject embedded placeholder, got %+v", rep.Offenders)
	}
	rep = Scan(map[string]any{"k": "${HOST}"}, whole())
	if len(rep.Offenders) != 0 || !reflect.DeepEqual(rep.Names, []string{"HOST"}) {
		t.Fatalf("whole mode should accept whole placeholder: %+v", rep)
	}
}

func TestRender(t *testing.T) {
	doc := map[string]any{
		"a": "${A}",
		"b": "x-${B}-y",
		"c": []any{"${A}", "plain"},
		"d": "$${A} stays",
	}
	values := map[string]string{"A": `k#ey: "with" \ weird 'q$1`, "B": "bee"}
	out, rep, err := Render(doc, DefaultSyntax(), MapLookup(values), RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["a"] != values["A"] {
		t.Errorf("a = %q", m["a"])
	}
	if m["b"] != "x-bee-y" {
		t.Errorf("b = %q", m["b"])
	}
	if m["c"].([]any)[0] != values["A"] {
		t.Errorf("c[0] = %q", m["c"].([]any)[0])
	}
	if m["d"] != "${A} stays" {
		t.Errorf("escape not honoured: %q", m["d"])
	}
	if !reflect.DeepEqual(rep.Substituted, []string{"A", "B"}) {
		t.Errorf("substituted = %v", rep.Substituted)
	}
	// original untouched
	if doc["a"] != "${A}" {
		t.Error("Render mutated its input")
	}
}

func TestRenderMissingAndFake(t *testing.T) {
	doc := map[string]any{"a": "${A}", "b": "${B}"}
	_, rep, err := Render(doc, DefaultSyntax(), MapLookup(map[string]string{"A": ""}), RenderOptions{})
	if err == nil || !strings.Contains(err.Error(), "A B") {
		t.Fatalf("expected both names reported as missing, got err=%v rep=%+v", err, rep)
	}
	out, rep, err := Render(doc, DefaultSyntax(), MapLookup(map[string]string{"A": "real"}), RenderOptions{Fake: true, FakePrefix: "fake-"})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["b"] != "fake-B" || !reflect.DeepEqual(rep.Faked, []string{"B"}) {
		t.Fatalf("fake substitution wrong: %+v %+v", out, rep)
	}
	if out.(map[string]any)["a"] != "real" {
		t.Fatal("real value must win over fake")
	}
}

func TestExpressions(t *testing.T) {
	doc := map[string]any{"a": "${A}", "b": "x-${B}", "lit": "has {{ braces }}"}
	out, rep, err := Expressions(doc, DefaultSyntax(), func(name string, whole bool) string {
		if whole {
			return "{{ ." + name + " | quote }}"
		}
		return "{{ ." + name + " }}"
	}, func(s string) string { return strings.ReplaceAll(s, "{{", "ESC") })
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["a"] != "{{ .A | quote }}" || m["b"] != "x-{{ .B }}" || m["lit"] != "has ESC braces }}" {
		t.Fatalf("unexpected: %+v", m)
	}
	if !reflect.DeepEqual(rep.Names, []string{"A", "B"}) {
		t.Fatalf("names = %v", rep.Names)
	}
}

func TestCustomSyntax(t *testing.T) {
	s := Syntax{Mode: ModeEmbedded, Open: "<<", Close: ">>", NameRE: regexp.MustCompile(`^[a-z]+$`)}
	rep := Scan(map[string]any{"k": "<<name>> and ${IGNORED}"}, s)
	if !reflect.DeepEqual(rep.Names, []string{"name"}) || len(rep.Offenders) != 0 {
		t.Fatalf("custom syntax: %+v", rep)
	}
}

func TestParseEnvFile(t *testing.T) {
	in := "# comment\n\nA=1\nexport B=\"two words\"\nC='single'\nD=has=equals\n"
	got, err := ParseEnvFile(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "single", "D": "has=equals"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, err := ParseEnvFile(strings.NewReader("novalue\n")); err == nil {
		t.Fatal("expected error for line without =")
	}
}
