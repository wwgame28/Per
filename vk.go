package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type APIError struct{ Code int }

func (e *APIError) Error() string {
	return fmt.Sprintf("VK API error %d; check token type, rights and community settings", e.Code)
}

type VKCaller interface {
	Call(context.Context, string, url.Values) (json.RawMessage, error)
}
type VK struct {
	Cfg    Config
	Client *http.Client
	Base   string
	mu     sync.Mutex
	next   time.Time
}

var userMethods = map[string]bool{"wall.get": true, "wall.getComments": true, "wall.post": true, "wall.edit": true, "wall.delete": true, "wall.deleteComment": true, "photos.getWallUploadServer": true, "photos.saveWallPhoto": true, "stats.get": true, "stats.getPostReach": true, "groups.ban": true, "groups.unban": true}

func newVK(c Config) *VK {
	return &VK{Cfg: c, Base: "https://api.vk.com/method/", Client: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (v *VK) Call(ctx context.Context, method string, p url.Values) (json.RawMessage, error) {
	token := v.Cfg.GroupToken
	if userMethods[method] {
		token = v.Cfg.UserToken
	}
	if token == "" {
		return nil, &APIError{27}
	}
	v.mu.Lock()
	wait := time.Until(v.next)
	if wait < 0 {
		wait = 0
	}
	v.next = time.Now().Add(wait + 350*time.Millisecond)
	v.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wait):
	}
	q := url.Values{}
	for k, vs := range p {
		q[k] = append([]string(nil), vs...)
	}
	q.Set("access_token", token)
	q.Set("v", "5.199")
	req, e := http.NewRequestWithContext(ctx, "POST", v.Base+method, strings.NewReader(q.Encode()))
	if e != nil {
		return nil, errors.New("VK request construction failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := v.Client.Do(req)
	if e != nil {
		return nil, errors.New("VK transport unavailable; outcome may be unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("VK HTTP failure; outcome may be unknown")
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
		Error    *struct {
			Code int `json:"error_code"`
		} `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); e != nil {
		return nil, errors.New("invalid VK response; outcome may be unknown")
	}
	if envelope.Error != nil {
		return nil, &APIError{envelope.Error.Code}
	}
	if len(envelope.Response) == 0 {
		return nil, errors.New("missing VK result")
	}
	return envelope.Response, nil
}

// Only a local, pre-provisioned basename may be uploaded. No model-controlled URL or file path.
func (v *VK) Upload(ctx context.Context, asset string) (json.RawMessage, error) {
	if filepath.Base(asset) != asset || asset == "." || strings.ContainsAny(asset, "/\\") {
		return nil, errors.New("asset must be a basename")
	}
	root, e := os.OpenRoot(v.Cfg.AssetsDir)
	if e != nil {
		return nil, errors.New("assets unavailable")
	}
	defer root.Close()
	f, e := root.Open(asset)
	if e != nil {
		return nil, errors.New("asset unavailable")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 || info.Size() < 1 {
		return nil, errors.New("asset must be a regular image up to 8 MiB")
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	mime := http.DetectContentType(head[:n])
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, errors.New("only JPEG or PNG supported")
	}
	f.Seek(0, 0)
	b, e := v.Call(ctx, "photos.getWallUploadServer", url.Values{"group_id": {number(v.Cfg.GroupID)}})
	if e != nil {
		return nil, e
	}
	var server struct {
		URL string `json:"upload_url"`
	}
	if json.Unmarshal(b, &server) != nil {
		return nil, errors.New("invalid upload server")
	}
	u, e := url.Parse(server.URL)
	if e != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !(strings.HasSuffix(u.Hostname(), ".vk.com") || strings.HasSuffix(u.Hostname(), ".vk.ru") || strings.HasSuffix(u.Hostname(), ".vkuserphoto.ru") || strings.HasSuffix(u.Hostname(), ".userapi.com")) {
		return nil, errors.New("untrusted VK upload host")
	}
	reader, writer := io.Pipe()
	multi := multipart.NewWriter(writer)
	go func() {
		defer writer.Close()
		part, err := multi.CreateFormFile("photo", asset)
		if err == nil {
			_, err = io.Copy(part, io.LimitReader(f, 8<<20))
		}
		if err == nil {
			err = multi.Close()
		}
		if err != nil {
			writer.CloseWithError(errors.New("photo stream failed"))
		}
	}()
	defer reader.Close()
	req, e := http.NewRequestWithContext(ctx, "POST", server.URL, reader)
	if e != nil {
		return nil, errors.New("upload request failed")
	}
	req.Header.Set("Content-Type", multi.FormDataContentType())
	resp, e := v.Client.Do(req)
	if e != nil {
		return nil, errors.New("photo upload failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("photo upload HTTP failure")
	}
	var uploaded struct {
		Server int64  `json:"server"`
		Photo  string `json:"photo"`
		Hash   string `json:"hash"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&uploaded) != nil || uploaded.Photo == "" || uploaded.Hash == "" {
		return nil, errors.New("invalid upload result")
	}
	return v.Call(ctx, "photos.saveWallPhoto", url.Values{"group_id": {number(v.Cfg.GroupID)}, "server": {number(uploaded.Server)}, "photo": {uploaded.Photo}, "hash": {uploaded.Hash}})
}
func values(m map[string]string) url.Values {
	p := url.Values{}
	for k, v := range m {
		p.Set(k, v)
	}
	return p
}
func strictJSON(s string, target any) error {
	d := json.NewDecoder(bytes.NewBufferString(s))
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return errors.New("invalid structured command")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
