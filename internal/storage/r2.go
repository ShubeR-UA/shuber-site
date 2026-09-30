package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// R2Client talks to the Worker outboundByHost handler using a private virtual
// hostname. The container never receives R2 access credentials.
type R2Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewR2Client(baseURL string) *R2Client {
	return &R2Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: &http.Client{Transport: http.DefaultTransport}}
}

func (c *R2Client) request(ctx context.Context, method, key string, body io.Reader, size int64, contentType string) (*http.Response, error) {
	if c == nil || c.BaseURL == "" {
		return nil, fmt.Errorf("r2 client is not configured")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	base.Path = "/" + strings.TrimLeft(key, "/")
	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.HTTPClient.Do(req)
}

func (c *R2Client) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.request(ctx, http.MethodGet, key, nil, 0, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, io.EOF
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("r2 get %q: %s: %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(resp.Body)
}

func (c *R2Client) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	resp, err := c.request(ctx, http.MethodPut, key, body, size, contentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("r2 put %q: %s: %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func (c *R2Client) PutBytes(ctx context.Context, key string, data []byte, contentType string) error {
	return c.Put(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
}

func (c *R2Client) Delete(ctx context.Context, key string) error {
	resp, err := c.request(ctx, http.MethodDelete, key, nil, 0, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("r2 delete %q: %s: %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
