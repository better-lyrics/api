package contracttest

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
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

func Run(t *testing.T, baseURL string, scenarios []Scenario) {
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
		})
	}
}
