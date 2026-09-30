package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindCreateUpdate(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			w.WriteHeader(401)
			return
		}
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "GET" && r.URL.Query().Get("page") == "1":
			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"id":1,"system":true,"body":"<!-- m --> system"},{"id":2,"body":"unrelated"}]`)
		case r.Method == "GET" && r.URL.Query().Get("page") == "2":
			fmt.Fprint(w, `[{"id":3,"body":"<!-- m -->\n### old"}]`)
		case r.Method == "PUT":
			_ = r.ParseForm()
			if r.Form.Get("body") != "new body" {
				t.Errorf("body = %q", r.Form.Get("body"))
			}
			fmt.Fprint(w, `{"id":3}`)
		case r.Method == "POST":
			fmt.Fprint(w, `{"id":9}`)
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	c, err := New(srv.Client(), srv.URL, "42", "7", "tok")
	if err != nil {
		t.Fatal(err)
	}
	id, found, err := c.FindNote(context.Background(), "<!-- m -->")
	if err != nil || !found || id != "3" {
		t.Fatalf("find = %q %v %v", id, found, err)
	}
	if err := c.UpdateNote(context.Background(), id, "new body"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateNote(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got[0], "GET /projects/42/merge_requests/7/notes?per_page=100&order_by=created_at&sort=desc&page=1") {
		t.Fatalf("first request = %s", got[0])
	}
	if got[2] != "PUT /projects/42/merge_requests/7/notes/3?" {
		t.Fatalf("update request = %s", got[2])
	}
}

func TestMissingSettings(t *testing.T) {
	if _, err := New(nil, "", "42", "", "tok"); err == nil || !strings.Contains(err.Error(), "CI_MERGE_REQUEST_IID") {
		t.Fatalf("expected missing settings error, got %v", err)
	}
}

func TestHTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
	}))
	defer srv.Close()
	c, _ := New(srv.Client(), srv.URL, "1", "1", "bad")
	if _, _, err := c.FindNote(context.Background(), "m"); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}
