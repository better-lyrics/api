package middleware

import (
	"fmt"
	"lyrics-api-go/stats"
	"net/http"
	"time"
)

// ResponseRecorder is a custom response writer that captures the status code and response size
type ResponseRecorder struct {
	http.ResponseWriter
	StatusCode int
	BodySize   int
}

// NewResponseRecorder creates a new instance of ResponseRecorder
func NewResponseRecorder(w http.ResponseWriter) *ResponseRecorder {
	return &ResponseRecorder{
		ResponseWriter: w,
		StatusCode:     http.StatusOK, // default status code
	}
}

// WriteHeader captures the status code
func (rec *ResponseRecorder) WriteHeader(code int) {
	rec.StatusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

// Write captures the size of the response body
func (rec *ResponseRecorder) Write(b []byte) (int, error) {
	size, err := rec.ResponseWriter.Write(b)
	rec.BodySize += size
	return size, err
}

// LoggingMiddleware logs the request details with colored status codes
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := NewResponseRecorder(w)
		start := time.Now()
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		// Record stats
		s := stats.Get()
		s.RecordRequest(r.URL.Path)
		s.RecordStatusCode(rec.StatusCode)
		s.RecordResponseTime(duration, r.URL.Path)
		s.RecordUserAgent(r.UserAgent())

		statusColor := getStatusColor(rec.StatusCode)
		resetColor := "\033[0m"

		fmt.Printf("%s %s %s %s%d%s %d %s\n",
			r.Method,
			r.URL,
			r.Proto,
			statusColor, rec.StatusCode, resetColor,
			rec.BodySize,
			duration,
		)
	})
}

// getStatusColor returns the color code for a given status code
func getStatusColor(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "\033[32m" // Green
	case status >= 300 && status < 400:
		return "\033[36m" // Cyan
	case status >= 400 && status < 500:
		return "\033[33m" // Yellow
	case status >= 500:
		return "\033[31m" // Red
	default:
		return "\033[0m" // Default
	}
}
