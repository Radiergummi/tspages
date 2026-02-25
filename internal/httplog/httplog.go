package httplog

import (
	"log/slog"
	"net/http"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Wrap returns an http.Handler that logs each request with method, path,
// status code, and duration. Extra slog attributes (e.g. site name) are
// prepended to every log line.
func Wrap(h http.Handler, attrs ...slog.Attr) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		h.ServeHTTP(rec, r)
		args := make([]any, 0, len(attrs)+4)
		for _, a := range attrs {
			args = append(args, a)
		}
		args = append(args, "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
		slog.Info("request", args...)
	})
}
