package healthcheck

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if err := Check(t.Context(), ok.URL); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), bad.URL); err == nil {
		t.Fatal("a 503 must be unhealthy")
	}
	ok.Close()
	if err := Check(t.Context(), ok.URL); err == nil {
		t.Fatal("a refused connection must be unhealthy")
	}
}
