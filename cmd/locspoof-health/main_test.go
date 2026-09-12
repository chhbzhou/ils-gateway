package main

import "testing"

func TestParseHealthyResponse(t *testing.T) {
	if err := parseHealthyResponse([]byte(`{"ok":true,"result":{"healthy":true}}`)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"ok":false}`, `{"ok":true,"result":{"healthy":false}}`, `bad`} {
		if err := parseHealthyResponse([]byte(body)); err == nil {
			t.Fatalf("accepted unhealthy response %s", body)
		}
	}
}
