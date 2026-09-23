package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchLimit_RejectsOversizedBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := fetchLimit(srv.Client(), srv.URL+"/big", 10)
	if err == nil {
		t.Fatal("expected an error for a body exceeding the limit")
	}
}

func TestFetchLimit_AllowsBodyAtOrUnderLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, err := fetchLimit(srv.Client(), srv.URL+"/ok", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "0123456789" {
		t.Errorf("body = %q, want %q", body, "0123456789")
	}
}
