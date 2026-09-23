package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestSpecIsValidOpenAPI31(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(Spec)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !doc.IsOpenAPI31OrLater() {
		t.Fatalf("openapi version = %q, want 3.1.x", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestHandlerServesSpecAsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	body := rec.Body.Bytes()
	doc, err := openapi3.NewLoader().LoadFromData(body)
	if err != nil {
		t.Fatalf("served JSON does not load as OpenAPI: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("served JSON is not a valid document: %v", err)
	}

	var served struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/getLyrics", "/ttml/getLyrics", "/qq/getLyrics", "/kugou/getLyrics", "/revalidate", "/health", "/cache/lookup", "/cache/clear/{provider}"} {
		if _, ok := served.Paths[p]; !ok {
			t.Errorf("served spec is missing path %s", p)
		}
	}
}

func TestSharedYAMLAnchorsExpandInServedJSON(t *testing.T) {
	var served struct {
		Paths map[string]struct {
			Get struct {
				Parameters []map[string]any `json:"parameters"`
				Deprecated bool             `json:"deprecated"`
			} `json:"get"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(specJSON, &served); err != nil {
		t.Fatal(err)
	}
	if got := len(served.Paths["/qq/getLyrics"].Get.Parameters); got != 11 {
		t.Errorf("/qq/getLyrics has %d parameters, want the 11 shared provider parameters", got)
	}
	if !served.Paths["/cache/dump"].Get.Deprecated {
		t.Error("/cache/dump should inherit the deprecated retired operation")
	}
}

func TestMustJSONPanicsOnInvalidYAML(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on invalid YAML")
		}
	}()
	mustJSON([]byte("paths: [unclosed"))
}

func TestServedJSONKeepsYAMLKeyOrder(t *testing.T) {
	got := string(mustJSON([]byte("b: 1\na:\n  z: true\n  y: [2, x]\n'404': null\n'200': 1.5\n")))
	want := `{"b":1,"a":{"z":true,"y":[2,"x"]},"404":null,"200":1.5}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestServedJSONKeepsSpecPathOrder(t *testing.T) {
	first := bytes.Index(specJSON, []byte(`"/getLyrics"`))
	later := bytes.Index(specJSON, []byte(`"/cache"`))
	if first < 0 || later < 0 || first > later {
		t.Fatalf("/getLyrics (at %d) should come before /cache (at %d), as in openapi.yaml", first, later)
	}
}

func TestMustJSONExpandsAliases(t *testing.T) {
	got := string(mustJSON([]byte("a: &x {k: v}\nb: *x\n")))
	want := `{"a":{"k":"v"},"b":{"k":"v"}}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
