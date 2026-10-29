package cmdutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// REST calls the server's REST endpoints (conversations, AG-UI, webhooks).
type REST struct {
	BaseURL string
	HTTP    *http.Client
}

// StatusError is a non-2xx response.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Body)
}

// Do sends a request and returns the response when the status is 2xx. The
// caller closes the body.
func (r *REST) Do(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, r.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	return resp, nil
}

// JSON sends in as a JSON body, when it is not nil, and decodes the response into out, when it is not nil.
func (r *REST) JSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	header := http.Header{"Accept": {"application/json"}}
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		header.Set("Content-Type", "application/json")
	}
	resp, err := r.Do(ctx, method, path, body, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
