package contracttest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"lyrics-api-go/internal/openapi"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

type Scenario struct {
	Name           string
	Method         string
	Path           string
	Headers        map[string]string
	Body           string
	WantStatus     int
	WantHeaders    map[string]string
	PresentHeaders []string
	AbsentHeaders  []string
	WantBody       string
	WantBodyRegex  *regexp.Regexp
}

// Run checks each scenario's expectations.
func Run(t *testing.T, baseURL string, scenarios []Scenario) {
	t.Helper()
	run(t, baseURL, scenarios, false)
}

// RunSpec is Run plus validation of every response against openapi.yaml. Use it for
// the deployed server; the spec does not describe the legacy BoltDB server.
func RunSpec(t *testing.T, baseURL string, scenarios []Scenario) {
	t.Helper()
	run(t, baseURL, scenarios, true)
}

func run(t *testing.T, baseURL string, scenarios []Scenario, validateSpec bool) {
	t.Helper()
	client := &http.Client{}
	for _, s := range scenarios {
		t.Run(s.Name, func(t *testing.T) {
			method := s.Method
			if method == "" {
				method = http.MethodGet
			}
			var body io.Reader
			if s.Body != "" {
				body = strings.NewReader(s.Body)
			}
			req, err := http.NewRequest(method, baseURL+s.Path, body)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			for k, v := range s.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if resp.StatusCode != s.WantStatus {
				t.Errorf("status: got %d want %d\nbody: %q", resp.StatusCode, s.WantStatus, got)
			}
			for k, v := range s.WantHeaders {
				if g := resp.Header.Get(k); g != v {
					t.Errorf("header %q: got %q want %q", k, g, v)
				}
			}
			for _, k := range s.PresentHeaders {
				if _, ok := resp.Header[http.CanonicalHeaderKey(k)]; !ok {
					t.Errorf("header %q: want present, was absent", k)
				}
			}
			for _, k := range s.AbsentHeaders {
				if _, ok := resp.Header[http.CanonicalHeaderKey(k)]; ok {
					t.Errorf("header %q: want absent, got %q", k, resp.Header.Get(k))
				}
			}
			if s.WantBodyRegex != nil {
				if !s.WantBodyRegex.Match(got) {
					t.Errorf("body: %q did not match /%s/", got, s.WantBodyRegex)
				}
			} else if s.WantBody != "" && string(got) != s.WantBody {
				t.Errorf("body: got %q want %q", got, s.WantBody)
			}
			if !validateSpec {
				return
			}
			if err := validateAgainstSpec(req, resp, got); err != nil {
				t.Errorf("response does not match openapi.yaml: %v", err)
			}
		})
	}
}

var specRouter = sync.OnceValues(func() (routers.Router, error) {
	doc, err := openapi3.NewLoader().LoadFromData(openapi.Spec)
	if err != nil {
		return nil, err
	}
	// Match the test server's host instead of the production server URL.
	doc.Servers = nil
	return gorillamux.NewRouter(doc)
})

// validateAgainstSpec checks a real response against openapi.yaml. Routes the spec
// does not document are skipped; documented routes must return a declared status,
// content type, headers and body.
func validateAgainstSpec(req *http.Request, resp *http.Response, body []byte) error {
	router, err := specRouter()
	if err != nil {
		return err
	}
	route, pathParams, err := router.FindRoute(req)
	if errors.Is(err, routers.ErrPathNotFound) || errors.Is(err, routers.ErrMethodNotAllowed) {
		return nil
	}
	if err != nil {
		return err
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status:  resp.StatusCode,
		Header:  resp.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true},
	}
	in.SetBodyBytes(body)
	return openapi3filter.ValidateResponse(context.Background(), in)
}
