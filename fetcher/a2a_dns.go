// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/agntcy/dir-importer/types"
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
	return 10
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

func (f *a2aDNSFetcher) fetchDomain(ctx context.Context, domain string, itemCh chan<- types.SourceItem, errCh chan<- error) {
	// Phase 1: SVCB query (primary path per DNS-AID).
	rrs, err := f.cfg.SVCBLookup(ctx, domain)
	if err != nil {
		dnsErr, ok := err.(*net.DNSError)
		if ok && dnsErr.IsNotFound {
			// NXDOMAIN or NODATA: this domain has no SVCB record, try TXT.
			goto txtFallback
		}
		// Real network failure: surface it and stop — TXT would likely fail too.
		select {
		case errCh <- fmt.Errorf("SVCB lookup for %s: %w", domain, err):
		case <-ctx.Done():
		}
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
		dnsErr, ok := err.(*net.DNSError)
		if ok && dnsErr.IsNotFound {
			return // Not an ANS-registered agent.
		}
		select {
		case errCh <- fmt.Errorf("TXT lookup for _ans.%s: %w", domain, err):
		case <-ctx.Done():
		}
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
			select {
			case errCh <- fmt.Errorf("_ans.%s: record has no url= field", domain):
			case <-ctx.Done():
			}
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
		select {
		case errCh <- fmt.Errorf("build request for %s: %w", cardURL, err):
		case <-ctx.Done():
		}
		return
	}

	resp, err := f.cfg.Client.Do(req)
	if err != nil {
		select {
		case errCh <- fmt.Errorf("fetch %s: %w", cardURL, err):
		case <-ctx.Done():
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		select {
		case errCh <- fmt.Errorf("fetch %s: HTTP %d", cardURL, resp.StatusCode):
		case <-ctx.Done():
		}
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		select {
		case errCh <- fmt.Errorf("read body from %s: %w", cardURL, err):
		case <-ctx.Done():
		}
		return
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		select {
		case errCh <- fmt.Errorf("decode JSON from %s: %w", cardURL, err):
		case <-ctx.Done():
		}
		return
	}

	s, err := structpb.NewStruct(m)
	if err != nil {
		select {
		case errCh <- fmt.Errorf("proto-encode card from %s: %w", cardURL, err):
		case <-ctx.Done():
		}
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
		for _, v := range alpn.Alpn {
			if v == "a2a" {
				return true
			}
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
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		idx := strings.IndexByte(part, '=')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(part[:idx])
		v := strings.Trim(strings.TrimSpace(part[idx+1:]), `"`)
		m[k] = v
	}
	return m
}

// containsA2A reports whether "a2a" appears in protocols (after trimming spaces).
func containsA2A(protocols []string) bool {
	for _, p := range protocols {
		if strings.TrimSpace(p) == "a2a" {
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
		return nil, err
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
	return net.DefaultResolver.LookupTXT(ctx, name)
}
