package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchElevation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("latitude") != "12.5" || r.URL.Query().Get("longitude") != "34.5" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = fmt.Fprint(w, `{"elevation":[21.0]}`)
	}))
	defer server.Close()
	elevation, err := fetchElevation(context.Background(), server.Client(), server.URL, 12.5, 34.5)
	if err != nil || elevation != 21 {
		t.Fatalf("elevation=%v err=%v", elevation, err)
	}
}

func TestFetchElevationRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"elevation":[]}`)
	}))
	defer server.Close()
	if _, err := fetchElevation(context.Background(), server.Client(), server.URL, 0, 0); err == nil {
		t.Fatal("invalid response was accepted")
	}
}
