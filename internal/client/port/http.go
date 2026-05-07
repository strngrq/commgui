package port

import (
	"io"
)

// HTTPClient — абстракция HTTP-клиента.
type HTTPClient interface {
	Do(req HTTPRequest) (*HTTPResponse, error)
}

// HTTPRequest — параметры HTTP-запроса.
type HTTPRequest struct {
	Method string
	URL    string
	Header map[string]string
	Body   io.Reader
}

// HTTPResponse — HTTP-ответ.
type HTTPResponse struct {
	StatusCode int
	Header     map[string]string
	Body       io.ReadCloser
}
