package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"webssh/internal/appwin"
)

func TestReopenExisting(t *testing.T) {
	const token = "test-token"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://127.0.0.1:8022/api/lockstate" || r.Header.Get("X-Auth-Token") != token {
			return response(http.StatusUnauthorized), nil
		}
		return response(http.StatusOK), nil
	})}

	var opened string
	found, err := reopenExistingWithClient("127.0.0.1:8022", token, func(rawURL string) error {
		opened = rawURL
		return nil
	}, client)
	if err != nil || !found {
		t.Fatalf("reopenExisting() = found %v, err %v", found, err)
	}
	if want := "http://127.0.0.1:8022/?token=" + token; opened != want {
		t.Fatalf("opened URL = %q, want %q", opened, want)
	}
}

func TestReopenExistingRejectsOtherService(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusNotFound), nil
	})}
	called := false
	found, err := reopenExistingWithClient("127.0.0.1:8022", "test-token", func(string) error {
		called = true
		return nil
	}, client)
	if err != nil || found || called {
		t.Fatalf("reopenExisting() = found %v, err %v, opened %v", found, err, called)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}
}

type fakeUI struct {
	mode appwin.Mode
	err  error
}

func (f *fakeUI) Open(string) error { return f.err }
func (f *fakeUI) Blocking() bool    { return false }
func (f *fakeUI) Close()            {}
func (f *fakeUI) Mode() appwin.Mode { return f.mode }

func TestAppWindowFallsBackWhenNoClientConnects(t *testing.T) {
	fallbacks := 0
	err := openNonBlockingUI(
		&fakeUI{mode: appwin.ModeApp}, "http://example.test/?token=x", func() bool { return false }, 0,
		func(string) error { fallbacks++; return nil },
	)
	if err != nil || fallbacks != 1 {
		t.Fatalf("openNonBlockingUI() = err %v, fallbacks %d; want nil, 1", err, fallbacks)
	}
}

func TestConnectedAppWindowDoesNotFallBack(t *testing.T) {
	fallbacks := 0
	err := openNonBlockingUI(
		&fakeUI{mode: appwin.ModeApp}, "http://example.test/?token=x", func() bool { return true }, time.Second,
		func(string) error { fallbacks++; return nil },
	)
	if err != nil || fallbacks != 0 {
		t.Fatalf("openNonBlockingUI() = err %v, fallbacks %d; want nil, 0", err, fallbacks)
	}
}

func TestAppWindowStartErrorFallsBack(t *testing.T) {
	wantErr := errors.New("browser fallback failed")
	err := openNonBlockingUI(
		&fakeUI{mode: appwin.ModeApp, err: errors.New("chromium failed")}, "http://example.test",
		func() bool { return false }, time.Second, func(string) error { return wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("openNonBlockingUI() error = %v, want %v", err, wantErr)
	}
}
