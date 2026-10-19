// Package s3test provides an isolated S3 HTTP fixture for backend and CLI tests.
package s3test

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const AccessKey = "erwtensoep-test-key"
const SecretKey = "erwtensoep-test-secret"

type Request struct {
	Method, Path, Query, Authorization, Token, Encoding string
}

type Server struct {
	*httptest.Server
	mu       sync.Mutex
	objects  map[string][]byte
	requests []Request
	faults   map[string]fault
}

type fault struct {
	code   string
	status int
}

func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{objects: make(map[string][]byte), faults: make(map[string]fault)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) Put(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
}

func (s *Server) Snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string][]byte)
	for key, data := range s.objects {
		result[key] = append([]byte(nil), data...)
	}
	return result
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) Fail(method, key, code string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[method+" "+key] = fault{code, status}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{r.Method, r.URL.Path, r.URL.RawQuery,
		r.Header.Get("Authorization"), r.Header.Get("X-Amz-Security-Token"), r.Header.Get("Content-Encoding")})
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential="+AccessKey+"/") {
		s3Error(w, "AccessDenied", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/bucket" && r.URL.Path != "/bucket/" && !strings.HasPrefix(r.URL.Path, "/bucket/") {
		s3Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/bucket"), "/")
	if f, ok := s.faults[r.Method+" "+key]; ok {
		s3Error(w, f.code, f.status)
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		var keys []string
		for name := range s.objects {
			if strings.HasPrefix(name, r.URL.Query().Get("prefix")) {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		start := 0
		if token := r.URL.Query().Get("continuation-token"); token != "" {
			var err error
			start, err = strconv.Atoi(strings.TrimPrefix(token, "page+/="))
			if err != nil || start < 0 || start > len(keys) {
				s3Error(w, "InvalidToken", http.StatusBadRequest)
				return
			}
		}
		end := min(start+2, len(keys)) // Force pagination on small test archives.
		type object struct {
			Key  string
			Size int
		}
		result := struct {
			XMLName               xml.Name `xml:"ListBucketResult"`
			IsTruncated           bool
			NextContinuationToken string `xml:",omitempty"`
			Contents              []object
		}{IsTruncated: end < len(keys)}
		if result.IsTruncated {
			result.NextContinuationToken = fmt.Sprintf("page+/=%d", end)
		}
		for _, name := range keys[start:end] {
			result.Contents = append(result.Contents, object{name, len(s.objects[name])})
		}
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(result)
		return
	}
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil || r.Header.Get("Content-Encoding") == "aws-chunked" || r.ContentLength != int64(len(data)) {
			s3Error(w, "InvalidRequest", http.StatusBadRequest)
			return
		}
		s.objects[key] = data
		w.Header().Set("ETag", `"test-etag"`)
	case http.MethodDelete:
		delete(s.objects, key)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet, http.MethodHead:
		data, ok := s.objects[key]
		if !ok {
			s3Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	default:
		s3Error(w, "NotImplemented", http.StatusNotImplemented)
	}
}

func s3Error(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
		Message string
	}{Code: code, Message: "test S3 error"})
}
