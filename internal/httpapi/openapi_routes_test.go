package httpapi

import (
	"sort"
	"testing"

	"lyrics-api-go/internal/openapi"

	"github.com/gorilla/mux"
	"go.yaml.in/yaml/v3"
)

// Routes deliberately left out of the public spec. Adding a route forces a choice:
// document it in openapi.yaml or list it here.
var undocumentedRoutes = map[string]bool{
	"/":                                 true,
	"/override":                         true,
	"/legacy/getLyrics":                 true,
	"/video-map":                        true,
	"/metadata":                         true,
	"/metadata/stats":                   true,
	"/metadata/sample":                  true,
	"/health/mut":                       true,
	"/stats":                            true,
	"/circuit-breaker":                  true,
	"/circuit-breaker/reset":            true,
	"/circuit-breaker/simulate-failure": true,
	"/test-notifications":               true,
}

func routerPaths(t *testing.T) map[string]bool {
	t.Helper()
	router := mux.NewRouter()
	(&Server{}).setupRoutes(router)
	paths := map[string]bool{}
	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tpl, err := route.GetPathTemplate()
		if err != nil {
			return err
		}
		paths[tpl] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func specPaths(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openapi.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for p := range doc.Paths {
		paths[p] = true
	}
	return paths
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOpenAPISpecPathsAreAllRouted(t *testing.T) {
	routed := routerPaths(t)
	for _, p := range sortedKeys(specPaths(t)) {
		if !routed[p] {
			t.Errorf("openapi.yaml documents %s but the router has no such route", p)
		}
	}
}

func TestEveryRouteIsDocumentedOrExplicitlyUndocumented(t *testing.T) {
	documented := specPaths(t)
	for _, p := range sortedKeys(routerPaths(t)) {
		if documented[p] && undocumentedRoutes[p] {
			t.Errorf("%s is documented in openapi.yaml and also listed as undocumented", p)
		}
		if !documented[p] && !undocumentedRoutes[p] {
			t.Errorf("route %s is neither in openapi.yaml nor in undocumentedRoutes", p)
		}
	}
}

func TestUndocumentedRoutesStillExist(t *testing.T) {
	routed := routerPaths(t)
	for _, p := range sortedKeys(undocumentedRoutes) {
		if !routed[p] {
			t.Errorf("undocumentedRoutes lists %s but the router has no such route", p)
		}
	}
}
