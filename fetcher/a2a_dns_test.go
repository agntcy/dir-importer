// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agntcy/dir-importer/types"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalCard is a valid A2A agent card JSON used across tests.
const minimalCard = `{"name":"Test Agent","url":"https://test.example","version":"1.0.0"}`

// testDomain is the default agent FQDN used across unit tests.
const testDomain = "agent.example.com"

// cardServer starts an httptest.TLS server that serves minimalCard at
// /.well-known/agent-card.json. Using TLS matches the https:// scheme that
// fetchDomain prepends to bare SVCB hostnames; srv.Client() carries the
// self-signed certificate so the fetcher can verify it in tests.
func cardServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent-card.json" {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, minimalCard)
	}))
	t.Cleanup(srv.Close)

	return srv, srv.URL
}

// svcbRR builds a dns.SVCB record with the given alpn values.
func svcbRR(target string, alpns []string) *dns.SVCB {
	rr := &dns.SVCB{
		Hdr:      dns.RR_Header{Rrtype: dns.TypeSVCB},
		Priority: 1,
		Target:   target,
		Value:    []dns.SVCBKeyValue{&dns.SVCBAlpn{Alpn: alpns}},
	}

	return rr
}

// txtLookupFunc returns a TXTLookup function that serves canned responses.
// Use sentinel value "NXDOMAIN" to simulate not-found; "ERROR" for network error.
func txtLookupFunc(m map[string][]string) func(ctx context.Context, name string) ([]string, error) {
	return func(ctx context.Context, name string) ([]string, error) {
		v, ok := m[name]
		if !ok {
			return nil, &net.DNSError{Name: name, IsNotFound: true}
		}

		if len(v) == 1 && v[0] == "ERROR" {
			return nil, &net.DNSError{Name: name, IsTimeout: true}
		}

		return v, nil
	}
}

// svcbLookupFunc returns a SVCBLookup function that serves canned responses.
// nil slice → NODATA (IsNotFound); "ERROR" sentinel → network error.
func svcbLookupFunc(m map[string][]dns.RR) func(ctx context.Context, domain string) ([]dns.RR, error) {
	return func(ctx context.Context, domain string) ([]dns.RR, error) {
		rrs, ok := m[domain]
		if !ok {
			return nil, &net.DNSError{Name: domain, IsNotFound: true}
		}

		if len(rrs) == 1 {
			if _, isErr := rrs[0].(*dns.MX); isErr { // use MX as sentinel for "ERROR"
				return nil, &net.DNSError{Name: domain, IsTimeout: true}
			}
		}

		return rrs, nil
	}
}

// collect drains itemCh and errCh into slices.
func collect(t *testing.T, itemCh <-chan types.SourceItem, errCh <-chan error) ([]types.SourceItem, []error) {
	t.Helper()

	var (
		items []types.SourceItem
		errs  []error
	)

	for itemCh != nil || errCh != nil {
		select {
		case item, ok := <-itemCh:
			if !ok {
				itemCh = nil

				continue
			}

			items = append(items, item)
		case err, ok := <-errCh:
			if !ok {
				errCh = nil

				continue
			}

			errs = append(errs, err)
		}
	}

	return items, errs
}

// --- Tests ---

func TestA2ADNSFetcher_SVCBHappyPath(t *testing.T) {
	srv, base := cardServer(t)
	// Strip https:// to get just host:port for the SVCB target.
	srvHost := strings.TrimPrefix(base, "https://")
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains: []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{
			domain: {svcbRR(srvHost+".", []string{"a2a", "h2"})},
		}),
		TXTLookup: txtLookupFunc(map[string][]string{}),
		Client:    srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	require.Len(t, items, 1)
	assert.Empty(t, errs)
	assert.Equal(t, types.SourceKindA2A, items[0].Kind)
}

func TestA2ADNSFetcher_SVCBNoA2A_FallsBackToTXT(t *testing.T) {
	srv, base := cardServer(t)
	domain := testDomain
	_ = base

	cfg := A2ADNSFetcherConfig{
		Domains: []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{
			domain: {svcbRR(".", []string{"h2"})}, // no a2a
		}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; version=v1.0.0; p=a2a; mode=direct; url=" + base},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	require.Len(t, items, 1)
	assert.Empty(t, errs)
	assert.Equal(t, types.SourceKindA2A, items[0].Kind)
}

func TestA2ADNSFetcher_SVCBNoDomain_FallsBackToTXT(t *testing.T) {
	srv, base := cardServer(t)
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}), // NXDOMAIN
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; version=v1.0.0; p=a2a; mode=direct; url=" + base},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	require.Len(t, items, 1)
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_SVCBNetworkError_NoTXTFallback(t *testing.T) {
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains: []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{
			domain: {&dns.MX{}}, // sentinel for network error
		}),
		TXTLookup: txtLookupFunc(map[string][]string{
			// TXT is valid — but should NOT be reached after SVCB network error
			"_ans." + domain: {"v=ans1; version=v1.0.0; p=a2a; mode=direct; url=https://agent.example.com"},
		}),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "SVCB lookup")
}

func TestA2ADNSFetcher_NeitherRecord(t *testing.T) {
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup:  txtLookupFunc(map[string][]string{}),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_MultiDomain(t *testing.T) {
	srv, base := cardServer(t)
	d1, d2 := "agent1.example.com", "agent2.example.com"

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{d1, d2},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + d1: {"v=ans1; p=a2a; url=" + base},
			"_ans." + d2: {"v=ans1; p=a2a; url=" + base},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Len(t, items, 2)
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_TXTMultiProtocol_A2AComma(t *testing.T) {
	srv, base := cardServer(t)
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a,mcp; url=" + base},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	require.Len(t, items, 1, "p=a2a,mcp must not be dropped")
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_TXTMCPOnly(t *testing.T) {
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=mcp; url=https://agent.example.com"},
		}),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_TXTMissingURL(t *testing.T) {
	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a"},
		}),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "url=")
}

func TestA2ADNSFetcher_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a; url=" + srv.URL},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "HTTP 404")
}

func TestA2ADNSFetcher_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{invalid json`)
	}))
	defer srv.Close()

	domain := testDomain

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a; url=" + srv.URL},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	assert.Empty(t, items)
	require.Len(t, errs, 1)
}

func TestA2ADNSFetcher_URLAlreadyHasCardPath(t *testing.T) {
	srv, base := cardServer(t)
	domain := testDomain
	cardURL := base + "/.well-known/agent-card.json"

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a; url=" + cardURL},
		}),
		Client: srv.Client(),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(context.Background())
	items, errs := collect(t, itemCh, errCh)

	require.Len(t, items, 1, "URL ending in agent-card.json must not be double-pathed")
	assert.Empty(t, errs)
}

func TestA2ADNSFetcher_ContextCancelled(t *testing.T) {
	domain := testDomain
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cfg := A2ADNSFetcherConfig{
		Domains:    []string{domain},
		SVCBLookup: svcbLookupFunc(map[string][]dns.RR{}),
		TXTLookup: txtLookupFunc(map[string][]string{
			"_ans." + domain: {"v=ans1; p=a2a; url=https://agent.example.com"},
		}),
	}

	f, err := NewA2ADNSFetcher(cfg)
	require.NoError(t, err)

	itemCh, errCh := f.Fetch(ctx)
	items, _ := collect(t, itemCh, errCh)
	assert.Empty(t, items) // cancelled ctx, no items expected
}

func TestNewA2ADNSFetcher_EmptyDomains(t *testing.T) {
	_, err := NewA2ADNSFetcher(A2ADNSFetcherConfig{Domains: []string{}})
	assert.Error(t, err)
}

func TestBuildCardURL(t *testing.T) {
	assert.Equal(t, "https://agent.example.com/.well-known/agent-card.json", buildCardURL("https://agent.example.com"))
	assert.Equal(t, "https://agent.example.com/.well-known/agent-card.json", buildCardURL("https://agent.example.com/"))
	assert.Equal(t, "https://agent.example.com/.well-known/agent-card.json", buildCardURL("https://agent.example.com/.well-known/agent-card.json"))
}

func TestResolveTarget(t *testing.T) {
	assert.Equal(t, "agent.example.com", resolveTarget(".", "agent.example.com"))
	assert.Equal(t, "agent.example.com", resolveTarget("agent.example.com.", "agent.example.com"))
	assert.Equal(t, "backend.example.com", resolveTarget("backend.example.com.", "agent.example.com"))
}

func TestParseKV(t *testing.T) {
	kv := parseKV("v=ans1; version=v1.0.9; p=a2a; mode=direct; url=https://agent.webmesh.ai")
	assert.Equal(t, "ans1", kv["v"])
	assert.Equal(t, "v1.0.9", kv["version"])
	assert.Equal(t, "a2a", kv["p"])
	assert.Equal(t, "https://agent.webmesh.ai", kv["url"])
}

func TestContainsA2A(t *testing.T) {
	assert.True(t, containsA2A([]string{"a2a"}))
	assert.True(t, containsA2A([]string{"a2a", "mcp"}))
	assert.True(t, containsA2A([]string{"mcp", " a2a "}))
	assert.False(t, containsA2A([]string{"mcp"}))
	assert.False(t, containsA2A([]string{}))
}
