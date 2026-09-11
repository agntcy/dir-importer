// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/agntcy/dir-importer/types"
	"github.com/miekg/dns"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	protoA2A = "a2a"
	protoMCP = "mcp"
)

// A2ADNSFetcherConfig configures the DNS-based A2A agent card fetcher.
// SVCBLookup and TXTLookup are injectable for testing; nil values use live DNS.
type A2ADNSFetcherConfig struct {
	// Domains lists agent FQDNs to query, e.g. "agent.webmesh.ai".
	Domains []string

	// SVCBLookup queries <domain> IN SVCB. Nil uses the system resolver via miekg/dns.
	SVCBLookup func(ctx context.Context, domain string) ([]dns.RR, error)

	// TXTLookup queries an arbitrary name for TXT records (used for _ans.<domain>).
	// Nil uses net.DefaultResolver.
	TXTLookup func(ctx context.Context, name string) ([]string, error)

	// Client is used for HTTP agent card fetches. Nil uses http.DefaultClient.
	Client *http.Client

	// Concurrency limits simultaneous goroutines. Zero defaults to 10.
	Concurrency int
}

func (c A2ADNSFetcherConfig) concurrencyLimit() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}

	return 10 //nolint:mnd // default goroutine cap
}

type a2aDNSFetcher struct {
	cfg A2ADNSFetcherConfig
}

// NewA2ADNSFetcher returns a Fetcher that discovers A2A agent cards via DNS.
// For each domain it queries the SVCB record for alpn=a2a first; if that
// yields nothing it falls back to the _ans.<domain> TXT record.
func NewA2ADNSFetcher(cfg A2ADNSFetcherConfig) (*a2aDNSFetcher, error) {
	if len(cfg.Domains) == 0 {
		return nil, fmt.Errorf("no domains provided")
	}

	if cfg.SVCBLookup == nil {
		cfg.SVCBLookup = defaultSVCBLookup
	}

	if cfg.TXTLookup == nil {
		cfg.TXTLookup = defaultTXTLookup
	}

	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}

	return &a2aDNSFetcher{cfg: cfg}, nil
}

// Fetch implements types.Fetcher. It queries each domain concurrently,
// bounded by concurrencyLimit, and emits SourceKindA2A items.
func (f *a2aDNSFetcher) Fetch(ctx context.Context) (<-chan types.SourceItem, <-chan error) {
	itemCh := make(chan types.SourceItem)
	errCh := make(chan error)

	go func() {
		sem := make(chan struct{}, f.cfg.concurrencyLimit())

		var wg sync.WaitGroup

		defer func() {
			wg.Wait()
			close(itemCh)
			close(errCh)
		}()

		for _, domain := range f.cfg.Domains {
			select {
			case <-ctx.Done():
				return
			default:
			}

			wg.Add(1)

			go func(d string) {
				defer wg.Done()

				sem <- struct{}{}

				defer func() { <-sem }()

				f.fetchDomain(ctx, d, itemCh, errCh)
			}(domain)
		}
	}()

	return itemCh, errCh
}

// sendErr delivers err to errCh, abandoning the send if ctx is cancelled.
func sendErr(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
	}
}

func (f *a2aDNSFetcher) fetchDomain(ctx context.Context, domain string, itemCh chan<- types.SourceItem, errCh chan<- error) {
	// Phase 1: SVCB query (primary path per DNS-AID).
	rrs, err := f.cfg.SVCBLookup(ctx, domain)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			// NXDOMAIN or NODATA: this domain has no SVCB record, try TXT.
			goto txtFallback
		}
		// Real network failure: surface it and stop — TXT would likely fail too.
		sendErr(ctx, errCh, fmt.Errorf("SVCB lookup for %s: %w", domain, err))

		return
	}

	for _, rr := range rrs {
		svcb, ok := rr.(*dns.SVCB)
		if !ok {
			continue
		}

		if !svcbHasA2A(svcb) {
			continue
		}

		host := resolveTarget(svcb.Target, domain)
		if !strings.Contains(host, "://") {
			host = "https://" + host
		}

		cardURL := buildCardURL(host)
		f.fetchAndEmit(ctx, cardURL, itemCh, errCh)

		return // SVCB path attempted (success or error already sent)
	}
	// SVCB present but no a2a alpn: fall through to TXT.

txtFallback:
	// Phase 2: _ans.<domain> TXT fallback.
	txts, err := f.cfg.TXTLookup(ctx, "_ans."+domain)

	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return // Not an ANS-registered agent.
		}

		sendErr(ctx, errCh, fmt.Errorf("TXT lookup for _ans.%s: %w", domain, err))

		return
	}

	for _, txt := range txts {
		if !strings.HasPrefix(txt, "v=ans1") {
			continue
		}

		kv := parseKV(txt)
		if !containsA2A(strings.Split(kv["p"], ",")) {
			continue
		}

		rawURL := kv["url"]
		if rawURL == "" {
			sendErr(ctx, errCh, fmt.Errorf("_ans.%s: record has no url= field", domain))

			return
		}

		f.fetchAndEmit(ctx, buildCardURL(rawURL), itemCh, errCh)

		return
	}
}

// fetchAndEmit fetches cardURL, decodes the JSON body as an A2A agent card,
// and sends it to itemCh. Errors go to errCh.
func (f *a2aDNSFetcher) fetchAndEmit(ctx context.Context, cardURL string, itemCh chan<- types.SourceItem, errCh chan<- error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		sendErr(ctx, errCh, fmt.Errorf("build request for %s: %w", cardURL, err))

		return
	}

	resp, err := f.cfg.Client.Do(req) //nolint:gosec // target URL derived from operator-configured domains
	if err != nil {
		sendErr(ctx, errCh, fmt.Errorf("fetch %s: %w", cardURL, err))

		return
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sendErr(ctx, errCh, fmt.Errorf("fetch %s: HTTP %d", cardURL, resp.StatusCode))

		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		sendErr(ctx, errCh, fmt.Errorf("read body from %s: %w", cardURL, err))

		return
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		sendErr(ctx, errCh, fmt.Errorf("decode JSON from %s: %w", cardURL, err))

		return
	}

	s, err := structpb.NewStruct(m)
	if err != nil {
		sendErr(ctx, errCh, fmt.Errorf("proto-encode card from %s: %w", cardURL, err))

		return
	}

	select {
	case itemCh <- types.A2ASourceItem(s):
	case <-ctx.Done():
	}
}

// svcbHasA2A reports whether the SVCB record's alpn SvcParam contains "a2a".
func svcbHasA2A(svcb *dns.SVCB) bool {
	for _, param := range svcb.Value {
		alpn, ok := param.(*dns.SVCBAlpn)
		if !ok {
			continue
		}

		if slices.Contains(alpn.Alpn, protoA2A) {
			return true
		}
	}

	return false
}

// resolveTarget converts a SVCB TargetName to a hostname.
// "." (or the owner name itself) means use the queried domain.
func resolveTarget(target, domain string) string {
	t := strings.TrimSuffix(target, ".")
	if t == "" || t == domain {
		return domain
	}

	return t
}

// buildCardURL appends /.well-known/agent-card.json to base unless it is
// already present.
func buildCardURL(base string) string {
	u := strings.TrimRight(base, "/")
	if strings.HasSuffix(u, "/agent-card.json") {
		return u
	}

	return u + "/.well-known/agent-card.json"
}

// parseKV parses a semicolon-delimited key=value string, stripping whitespace
// and surrounding double-quotes from each value.
func parseKV(s string) map[string]string {
	m := make(map[string]string)

	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)

		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"`)
		m[k] = v
	}

	return m
}

// containsA2A reports whether "a2a" appears in protocols (after trimming spaces).
func containsA2A(protocols []string) bool {
	for _, p := range protocols {
		if strings.TrimSpace(p) == protoA2A {
			return true
		}
	}

	return false
}

// defaultSVCBLookup queries <domain> IN SVCB using the system resolver.
func defaultSVCBLookup(ctx context.Context, domain string) ([]dns.RR, error) {
	conf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		conf = &dns.ClientConfig{Servers: []string{"8.8.8.8"}, Port: "53"}
	}

	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeSVCB)
	m.RecursionDesired = true

	server := fmt.Sprintf("%s:%s", conf.Servers[0], conf.Port)

	r, _, err := c.ExchangeContext(ctx, m, server)
	if err != nil {
		return nil, err //nolint:wrapcheck // DNS error types come from net/miekg; caller wraps with domain context
	}

	if r.Rcode == dns.RcodeNameError {
		return nil, &net.DNSError{Name: domain, IsNotFound: true}
	}

	if r.Rcode != dns.RcodeSuccess || len(r.Answer) == 0 {
		return nil, &net.DNSError{Name: domain, IsNotFound: true}
	}

	return r.Answer, nil
}

// defaultTXTLookup uses net.DefaultResolver to look up TXT records.
func defaultTXTLookup(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name) //nolint:wrapcheck // DNS error types come from net package; caller wraps with domain context
}
