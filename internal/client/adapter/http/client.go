package http

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/strngrq/commgui/internal/client/port"
	"github.com/strngrq/commgui/internal/cryptox"
	"github.com/strngrq/commgui/internal/proto"
	"github.com/strngrq/commgui/internal/wire"
)

type Client struct {
	signer func(*http.Request, []byte)
}

func NewClient() *Client {
	return &Client{}
}

func NewSignedClient(privKey ed25519.PrivateKey) *Client {
	c := &Client{}
	c.signer = func(req *http.Request, body []byte) {
		ts := time.Now().UnixMilli()
		req.Header.Set("X-Ts", fmt.Sprintf("%d", ts))
		sig := ed25519.Sign(privKey, wire.CanonicalRequest(req.Method, req.URL.RequestURI(), ts, body))
		req.Header.Set("X-Sig", cryptox.EncodeBase64URL(sig))
	}
	return c
}

func (c *Client) Do(req port.HTTPRequest) (*port.HTTPResponse, error) {
	var bodyReader io.Reader
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	httpReq, err := http.NewRequest(req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Header {
		httpReq.Header.Set(k, v)
	}
	if c.signer != nil {
		c.signer(httpReq, bodyBytes)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	return &port.HTTPResponse{
		StatusCode: resp.StatusCode,
		Header:     headers,
		Body:       resp.Body,
	}, nil
}

func DoJSON(client port.HTTPClient, method, url string, in any, out any, token string) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}

	req := port.HTTPRequest{
		Method: method,
		URL:    url,
		Header: map[string]string{},
		Body:   body,
	}
	req.Header["Content-Type"] = "application/json"
	if token != "" {
		req.Header["Authorization"] = "Bearer " + token
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var er proto.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error.Code != "" {
			return errors.New(er.Error.Code + ": " + er.Error.Message)
		}
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
