package yamlx

import (
	"strings"
	"testing"
)

func TestSortedIgnoresOrderAndQuoting(t *testing.T) {
	a := "b: 1\na: \"x\"\nlist:\n- z: 1\n  y: 2\n"
	b := "a: x\nlist:\n  - y: 2\n    z: 1\nb: 1\n"
	sa, err := Sorted([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	sb, err := Sorted([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if string(sa) != string(sb) {
		t.Fatalf("sorted forms differ:\n%s\n---\n%s", sa, sb)
	}
	if !strings.HasPrefix(string(sa), "a: x\nb: 1\n") {
		t.Fatalf("keys not sorted: %s", sa)
	}
}

func TestSortedKeepsMultilineStyle(t *testing.T) {
	in := "cert: |\n  line1\n  line2\n"
	out, err := Sorted([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "|") {
		t.Fatalf("literal style lost: %s", out)
	}
}

func TestCanonicalMatchesDeckStyle(t *testing.T) {
	m, err := Deserialize([]byte("services:\n  - name: a\n    port: 443\n_format_version: '3.0'\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	want := "_format_version: \"3.0\"\nservices:\n- name: a\n  port: 443\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}
