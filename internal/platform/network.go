package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultResponseLimit = int64(16 << 20)

type NetworkErrorKind string

const (
	NetworkInvalidRequest NetworkErrorKind = "invalid_request"
	NetworkHTTPSRequired  NetworkErrorKind = "https_required"
	NetworkRedirectPolicy NetworkErrorKind = "redirect_policy"
	NetworkCancelled      NetworkErrorKind = "cancelled"
	NetworkTimeout        NetworkErrorKind = "timeout"
	NetworkTransport      NetworkErrorKind = "transport"
	NetworkResponseLimit  NetworkErrorKind = "response_limit"
	NetworkResponseRead   NetworkErrorKind = "response_read"
)

type NetworkError struct {
	Kind NetworkErrorKind
	URL  string
	Err  error
}

func (err *NetworkError) Error() string {
	return fmt.Sprintf("network %s for %s", err.Kind, err.URL)
}

func (err *NetworkError) Unwrap() error {
	return err.Err
}

type HTTPNetwork struct {
	client *http.Client
}

func NewHTTPNetwork() *HTTPNetwork {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	return NewHTTPNetworkWithTransport(transport)
}

func NewHTTPNetworkWithTransport(transport http.RoundTripper) *HTTPNetwork {
	network := &HTTPNetwork{}
	network.client = &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return &NetworkError{Kind: NetworkRedirectPolicy, URL: safeNetworkURL(request.URL.String()), Err: errors.New("too many redirects")}
			}
			if !strings.EqualFold(request.URL.Scheme, "https") {
				return &NetworkError{Kind: NetworkRedirectPolicy, URL: safeNetworkURL(request.URL.String()), Err: errors.New("redirect downgraded from HTTPS")}
			}
			return nil
		},
	}
	return network
}

func (network *HTTPNetwork) Do(ctx context.Context, request Request) (Response, error) {
	parsed, err := url.Parse(request.URL)
	if err != nil || parsed.Host == "" {
		return Response{}, &NetworkError{Kind: NetworkInvalidRequest, URL: safeNetworkURL(request.URL), Err: err}
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return Response{}, &NetworkError{Kind: NetworkHTTPSRequired, URL: safeNetworkURL(request.URL), Err: errors.New("only HTTPS is allowed")}
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, request.URL, request.Body)
	if err != nil {
		return Response{}, &NetworkError{Kind: NetworkInvalidRequest, URL: safeNetworkURL(request.URL), Err: err}
	}
	for key, value := range request.Headers {
		httpRequest.Header.Set(key, value)
	}
	httpResponse, err := network.client.Do(httpRequest)
	if err != nil {
		var networkError *NetworkError
		if errors.As(err, &networkError) {
			return Response{}, networkError
		}
		kind := NetworkTransport
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			kind = NetworkCancelled
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = NetworkTimeout
		}
		return Response{}, &NetworkError{Kind: kind, URL: safeNetworkURL(request.URL), Err: err}
	}
	defer httpResponse.Body.Close()
	limit := request.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultResponseLimit
	}
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, limit+1))
	if err != nil {
		return Response{}, &NetworkError{Kind: NetworkResponseRead, URL: safeNetworkURL(request.URL), Err: err}
	}
	if int64(len(body)) > limit {
		return Response{}, &NetworkError{Kind: NetworkResponseLimit, URL: safeNetworkURL(request.URL), Err: fmt.Errorf("response exceeds %d bytes", limit)}
	}
	return Response{
		StatusCode: httpResponse.StatusCode,
		Headers:    cloneHTTPHeaders(httpResponse.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func safeNetworkURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "<invalid-url>"
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil {
		host = "<ip>"
	}
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return strings.ToLower(parsed.Scheme) + "://" + host
}

func cloneHTTPHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}
