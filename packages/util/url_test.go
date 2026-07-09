package util

import (
	"net/url"
	"testing"
)

func TestBuildURLMergesExistingQuery(t *testing.T) {
	type query struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	got, err := BuildURL("https://example.com/api?existing=1", query{Name: "alice", Age: 30})
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	values := parsed.Query()
	if values.Get("existing") != "1" || values.Get("name") != "alice" || values.Get("age") != "30" {
		t.Fatalf("query = %s", parsed.RawQuery)
	}
	if parsed.Scheme != "https" || parsed.Host != "example.com" || parsed.Path != "/api" {
		t.Fatalf("parsed URL = %+v", parsed)
	}
}
