package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetStatusColor(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   string
	}{
		{
			name:       "2xx Success - Green",
			statusCode: http.StatusOK,
			expected:   "\033[32m",
		},
		{
			name:       "201 Created - Green",
			statusCode: http.StatusCreated,
			expected:   "\033[32m",
		},
		{
			name:       "204 No Content - Green",
			statusCode: http.StatusNoContent,
			expected:   "\033[32m",
		},
		{
			name:       "3xx Redirect - Cyan",
			statusCode: http.StatusMovedPermanently,
			expected:   "\033[36m",
		},
		{
			name:       "302 Found - Cyan",
			statusCode: http.StatusFound,
			expected:   "\033[36m",
		},
		{
			name:       "304 Not Modified - Cyan",
			statusCode: http.StatusNotModified,
			expected:   "\033[36m",
		},
		{
			name:       "4xx Client Error - Yellow",
			statusCode: http.StatusBadRequest,
			expected:   "\033[33m",
		},
		{
			name:       "404 Not Found - Yellow",
			statusCode: http.StatusNotFound,
			expected:   "\033[33m",
		},
		{
			name:       "401 Unauthorized - Yellow",
			statusCode: http.StatusUnauthorized,
			expected:   "\033[33m",
		},
		{
			name:       "429 Too Many Requests - Yellow",
			statusCode: http.StatusTooManyRequests,
			expected:   "\033[33m",
		},
		{
			name:       "5xx Server Error - Red",
			statusCode: http.StatusInternalServerError,
			expected:   "\033[31m",
		},
		{
			name:       "502 Bad Gateway - Red",
			statusCode: http.StatusBadGateway,
			expected:   "\033[31m",
		},
		{
			name:       "503 Service Unavailable - Red",
			statusCode: http.StatusServiceUnavailable,
			expected:   "\033[31m",
		},
		{
			name:       "Edge case - 199 (below 2xx)",
			statusCode: 199,
			expected:   "\033[0m",
		},
		{
			name:       "Edge case - 100 Continue",
			statusCode: http.StatusContinue,
			expected:   "\033[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStatusColor(tt.statusCode)
			if result != tt.expected {
				t.Errorf("Expected color code %q for status %d, got %q", tt.expected, tt.statusCode, result)
			}
		})
	}
}

func TestNewResponseRecorder(t *testing.T) {
	w := httptest.NewRecorder()
	rec := NewResponseRecorder(w)

	if rec == nil {
		t.Fatal("Expected ResponseRecorder to be created, got nil")
	}

	if rec.StatusCode != http.StatusOK {
		t.Errorf("Expected default status code %d, got %d", http.StatusOK, rec.StatusCode)
	}

	if rec.BodySize != 0 {
		t.Errorf("Expected initial body size 0, got %d", rec.BodySize)
	}
}

func TestResponseRecorder_WriteHeader(t *testing.T) {
	w := httptest.NewRecorder()
	rec := NewResponseRecorder(w)

	statusCodes := []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusTooManyRequests,
	}

	for _, statusCode := range statusCodes {
		w = httptest.NewRecorder()
		rec = NewResponseRecorder(w)

		rec.WriteHeader(statusCode)

		if rec.StatusCode != statusCode {
			t.Errorf("Expected status code %d, got %d", statusCode, rec.StatusCode)
		}

		if w.Code != statusCode {
			t.Errorf("Expected underlying writer to have status code %d, got %d", statusCode, w.Code)
		}
	}
}

func TestResponseRecorder_Write(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		expectedSize int
	}{
		{
			name:         "Empty response",
			data:         []byte{},
			expectedSize: 0,
		},
		{
			name:         "Small response",
			data:         []byte("Hello, World!"),
			expectedSize: 13,
		},
		{
			name:         "JSON response",
			data:         []byte(`{"message":"success","data":{"id":1,"name":"test"}}`),
			expectedSize: 51,
		},
		{
			name:         "Large response",
			data:         []byte(string(make([]byte, 1024))),
			expectedSize: 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			rec := NewResponseRecorder(w)

			n, err := rec.Write(tt.data)
			if err != nil {
				t.Fatalf("Unexpected error writing response: %v", err)
			}

			if n != tt.expectedSize {
				t.Errorf("Expected to write %d bytes, wrote %d", tt.expectedSize, n)
			}

			if rec.BodySize != tt.expectedSize {
				t.Errorf("Expected body size %d, got %d", tt.expectedSize, rec.BodySize)
			}
		})
	}
}

func TestResponseRecorder_MultipleWrites(t *testing.T) {
	w := httptest.NewRecorder()
	rec := NewResponseRecorder(w)

	writes := [][]byte{
		[]byte("Hello"),
		[]byte(", "),
		[]byte("World"),
		[]byte("!"),
	}

	totalSize := 0
	for _, data := range writes {
		n, err := rec.Write(data)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		totalSize += n
	}

	if rec.BodySize != totalSize {
		t.Errorf("Expected total body size %d, got %d", totalSize, rec.BodySize)
	}

	expectedTotal := len("Hello") + len(", ") + len("World") + len("!")
	if rec.BodySize != expectedTotal {
		t.Errorf("Expected body size %d, got %d", expectedTotal, rec.BodySize)
	}
}

func TestLoggingMiddleware(t *testing.T) {
	// Create a simple test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Test response"))
	})

	// Wrap with logging middleware
	middleware := LoggingMiddleware(handler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Execute
	middleware.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if body != "Test response" {
		t.Errorf("Expected body 'Test response', got %q", body)
	}
}

func TestLoggingMiddleware_DifferentStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"Success", http.StatusOK},
		{"Created", http.StatusCreated},
		{"Not Found", http.StatusNotFound},
		{"Bad Request", http.StatusBadRequest},
		{"Internal Server Error", http.StatusInternalServerError},
		{"Too Many Requests", http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			middleware := LoggingMiddleware(handler)
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()

			middleware.ServeHTTP(rec, req)

			if rec.Code != tt.statusCode {
				t.Errorf("Expected status code %d, got %d", tt.statusCode, rec.Code)
			}
		})
	}
}

func TestLoggingMiddleware_DifferentMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := LoggingMiddleware(handler)
			req := httptest.NewRequest(method, "/test", nil)
			rec := httptest.NewRecorder()

			middleware.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status code %d for %s, got %d", http.StatusOK, method, rec.Code)
			}
		})
	}
}

func TestResponseRecorder_DefaultStatusCode(t *testing.T) {
	w := httptest.NewRecorder()
	rec := NewResponseRecorder(w)

	// Write without explicitly calling WriteHeader
	rec.Write([]byte("test"))

	// Should still have default status code
	if rec.StatusCode != http.StatusOK {
		t.Errorf("Expected default status code %d, got %d", http.StatusOK, rec.StatusCode)
	}
}
