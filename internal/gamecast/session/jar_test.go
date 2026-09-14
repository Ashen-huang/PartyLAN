package session

import (
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

func TestJarSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	baseURL := "https://example.com:5050"
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	jar, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "user", Value: "signed-cookie", Path: "/"}})
	if err := jar.Save(baseURL); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := New(path)
	if err != nil {
		t.Fatalf("New loaded: %v", err)
	}
	if err := loaded.Load(baseURL); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cookies := loaded.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != "user" || cookies[0].Value != "signed-cookie" {
		t.Fatalf("cookies=%#v", cookies)
	}
}

func TestJarClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	baseURL := "https://example.com:5050"
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	jar, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "user", Value: "signed-cookie", Path: "/"}})
	if err := jar.Save(baseURL); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := jar.Clear(baseURL); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	loaded, err := New(path)
	if err != nil {
		t.Fatalf("New loaded: %v", err)
	}
	if err := loaded.Load(baseURL); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cookies := loaded.Cookies(u); len(cookies) != 0 {
		t.Fatalf("cookies after clear=%#v", cookies)
	}
}
