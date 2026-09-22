// Package rpcpool dials EVM RPC endpoints with transparent HTTP failover.
//
// Public RPC gateways (e.g. publicnode behind Cloudflare) intermittently hang
// until the gateway's 120s proxy timeout, which stalls every caller sharing the
// ethclient. Dial wraps the JSON-RPC HTTP transport so each request gets a
// bounded per-attempt timeout and fails over to the next endpoint on transport
// errors or 5xx responses. Consumers keep using a plain *ethclient.Client.
package rpcpool

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// attemptTimeout bounds a single request to a single endpoint. Well below the
// 120s Cloudflare proxy window so a hung origin fails over instead of stalling.
const attemptTimeout = 20 * time.Second

// Dial connects to the first usable endpoint. With multiple http(s) URLs the
// returned client retries each JSON-RPC request across them; a single
// websocket URL is dialed directly (failover does not apply to ws).
func Dial(ctx context.Context, name string, urls []string) (*ethclient.Client, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("rpcpool %s: no endpoints configured", name)
	}
	if isWS(urls[0]) {
		if len(urls) > 1 {
			return nil, fmt.Errorf("rpcpool %s: failover requires http(s) endpoints; websocket URLs must be configured alone", name)
		}
		return ethclient.DialContext(ctx, urls[0])
	}
	for _, u := range urls {
		if isWS(u) {
			return nil, fmt.Errorf("rpcpool %s: cannot mix websocket endpoint %s into an http failover list", name, u)
		}
	}

	transport := &failoverTransport{name: name, urls: urls, base: http.DefaultTransport}
	httpClient := &http.Client{Transport: transport}
	rc, err := rpc.DialOptions(ctx, urls[0], rpc.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	return ethclient.NewClient(rc), nil
}

func isWS(u string) bool {
	return strings.HasPrefix(u, "ws://") || strings.HasPrefix(u, "wss://")
}

// failoverTransport retries a request across endpoints, remembering the last
// endpoint that worked so a dead primary is not re-tried first on every call.
type failoverTransport struct {
	name      string
	urls      []string
	base      http.RoundTripper
	preferred atomic.Int32
}

func (t *failoverTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := int(t.preferred.Load())
	var lastErr error
	for i := 0; i < len(t.urls); i++ {
		idx := (start + i) % len(t.urls)
		resp, err := t.tryEndpoint(req, idx)
		if err == nil && resp.StatusCode < 500 {
			if idx != start {
				t.preferred.Store(int32(idx))
				log.Printf("rpcpool %s: switched preferred endpoint to %s", t.name, t.urls[idx])
			}
			return resp, nil
		}
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d from %s", resp.StatusCode, t.urls[idx])
		}
		lastErr = err
		log.Printf("rpcpool %s: endpoint %s failed: %v", t.name, t.urls[idx], err)
		if req.Context().Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("rpcpool %s: all endpoints failed: %w", t.name, lastErr)
}

func (t *failoverTransport) tryEndpoint(req *http.Request, idx int) (*http.Response, error) {
	target, err := url.Parse(t.urls[idx])
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(req.Context(), attemptTimeout)
	clone := req.Clone(ctx)
	clone.URL = target
	clone.Host = target.Host
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			cancel()
			return nil, err
		}
		clone.Body = body
	}

	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		cancel()
		return nil, err
	}
	// The per-attempt context must stay alive until the caller finishes
	// reading the body; tie cancellation to Close instead.
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
