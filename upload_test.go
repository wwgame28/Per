package main

import (
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPhotoUploadStreamsThroughRealClient(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "sample.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	f.Close()
	uploaded := false
	saved := false
	v := newVK(Config{GroupID: 10, GroupToken: "secret-group", UserToken: "secret-user", AssetsDir: dir})
	v.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case strings.HasSuffix(r.URL.Path, "photos.getWallUploadServer"):
			body = `{"response":{"upload_url":"https://upload.vk.com/upload"}}`
		case r.URL.Host == "upload.vk.com":
			reader, e := r.MultipartReader()
			if e != nil {
				t.Fatal(e)
			}
			part, e := reader.NextPart()
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(part)
			if part.FormName() != "photo" || http.DetectContentType(b) != "image/png" {
				t.Fatal("invalid photo")
			}
			uploaded = true
			body = `{"server":1,"photo":"saved-image","hash":"upload-hash"}`
		case strings.HasSuffix(r.URL.Path, "photos.saveWallPhoto"):
			r.ParseForm()
			if r.Form.Get("group_id") != "10" || r.Form.Get("access_token") != "secret-user" {
				t.Fatal("wrong token/scope")
			}
			saved = true
			body = `{"response":[{"id":7,"owner_id":-10}]}`
		default:
			t.Fatalf("unexpected URL %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	result, err := v.Upload(context.Background(), "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if !uploaded || !saved || !strings.Contains(string(result), `"id":7`) {
		t.Fatal(string(result))
	}
	if _, err = v.Upload(context.Background(), "../sample.png"); err == nil {
		t.Fatal("path traversal")
	}
}
func TestRecentActivityIsLocalAndBounded(t *testing.T) {
	e, v, _ := fixture(t)
	a, err := e.submit(Intent{Tool: "vk.get_recent_activity"}, "activity", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.execute(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.action(a.ID)
	if !strings.Contains(got.Result, "locally received") || len(v.calls) != 0 {
		t.Fatal(got.Result)
	}
}
