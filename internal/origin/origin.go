package origin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

// Config for the Stage 3 constructor.
type Config struct {
	BaseURL string
	Timeout time.Duration
}

type Server struct {
	requestCount atomic.Int64
	minLatencyMs int
	maxLatencyMs int
}

// NewServer creates an origin server with simulated latency (Stage 1 API).
func NewServer(minLatencyMs, maxLatencyMs int) *Server {
	if minLatencyMs <= 0 {
		minLatencyMs = 50
	}
	if maxLatencyMs <= minLatencyMs {
		maxLatencyMs = minLatencyMs + 150
	}
	return &Server{minLatencyMs: minLatencyMs, maxLatencyMs: maxLatencyMs}
}

// New creates an origin server from a Config (Stage 3 API).
func New(cfg Config) *Server {
	return NewServer(50, 200)
}

type FetchResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
	Headers     http.Header
}

func (s *Server) Fetch(urlPath string) (*FetchResponse, error) {
	s.requestCount.Add(1)
	delay := s.randomLatency()
	time.Sleep(delay)
	contentType, body := s.generateContent(urlPath)
	headers := make(http.Header)
	headers.Set("X-Origin-Latency", delay.String())
	headers.Set("X-Origin-Request-Count", fmt.Sprintf("%d", s.requestCount.Load()))
	headers.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	return &FetchResponse{StatusCode: http.StatusOK, ContentType: contentType, Body: body, Headers: headers}, nil
}

func (s *Server) RequestCount() int64 { return s.requestCount.Load() }

func (s *Server) generateContent(urlPath string) (string, []byte) {
	ext := strings.ToLower(path.Ext(urlPath))
	cleanPath := strings.TrimPrefix(urlPath, "/")
	if cleanPath == "" {
		cleanPath = "index"
	}
	etag := generateETag(cleanPath)
	switch ext {
	case ".json", "":
		if strings.Contains(urlPath, "/api/") || ext == ".json" {
			return "application/json", generateJSON(cleanPath, etag)
		}
		return "text/html; charset=utf-8", generateHTML(cleanPath, etag)
	case ".css":
		return "text/css; charset=utf-8", generateCSS(cleanPath)
	case ".js":
		return "application/javascript; charset=utf-8", generateJS(cleanPath)
	case ".png", ".jpg", ".gif", ".webp":
		return "image/png", generateFakeImage(cleanPath)
	default:
		return "text/html; charset=utf-8", generateHTML(cleanPath, etag)
	}
}

func generateHTML(pagePath, etag string) []byte {
	return []byte(fmt.Sprintf(`<!DOCTYPE html><html><head><title>%s</title></head><body><h1>%s</h1><p>Origin %s ETag:%s</p></body></html>`, pagePath, pagePath, time.Now().Format(time.RFC3339), etag))
}

func generateJSON(apiPath, etag string) []byte {
	return []byte(fmt.Sprintf(`{"path":"%s","status":"ok","server":"origin-v2","timestamp":"%s","etag":"%s","data":{"items":[{"id":1,"name":"Alpha","value":42.5}],"total":1}}`, apiPath, time.Now().Format(time.RFC3339), etag))
}

func generateCSS(cssPath string) []byte {
	return []byte(fmt.Sprintf(`/* %s */ * { margin:0; padding:0; box-sizing:border-box; }`, cssPath))
}

func generateJS(jsPath string) []byte {
	return []byte(fmt.Sprintf(`(function(){'use strict';console.log('%s');})();`, jsPath))
}

func generateFakeImage(_ string) []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
}

func generateETag(s string) string {
	_ = s
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) randomLatency() time.Duration {
	spread := s.maxLatencyMs - s.minLatencyMs
	if spread <= 0 {
		return time.Duration(s.minLatencyMs) * time.Millisecond
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(spread)))
	if err != nil {
		return time.Duration(s.minLatencyMs+spread/2) * time.Millisecond
	}
	return time.Duration(s.minLatencyMs+int(n.Int64())) * time.Millisecond
}
