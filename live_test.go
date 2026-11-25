package verifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveActions(t *testing.T) {
	host := os.Getenv("ACTIONS_API_URL")
	cases := os.Getenv("ACTIONS_API_CASES")
	key := os.Getenv("ACTIONS_API_KEY")
	base, err := liveActionsURL(host, cases, key)
	if err != nil {
		t.Fatal(err)
	}
	if base == nil {
		t.Skip("set ACTIONS_API_URL and ACTIONS_API_CASES to enable live requests")
	}
	input, err := os.ReadFile(cases)
	if err != nil {
		t.Fatal("set ACTIONS_API_CASES to a readable JSON array of intents")
	}
	var intents []Intent
	if json.Unmarshal(input, &intents) != nil || len(intents) == 0 {
		t.Fatal("ACTIONS_API_CASES must contain a nonempty JSON array of intents")
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var denied bool
	for _, intent := range intents {
		var body request
		if json.Unmarshal(intent.Body, &body) != nil {
			t.Error("invalid test intent body")
			continue
		}
		r, ok := parseRoute(intent.Path, body.ChainID)
		if !ok || !nonzeroAddress(body.Account) {
			t.Error("invalid test intent")
			continue
		}
		t.Run(r.family+"/"+r.action, func(t *testing.T) {
			if denied {
				t.Skipf("chain %d: not run after authentication failure", r.chain)
			}
			denied = checkLive(t, client, strings.TrimRight(base.String(), "/"), key, intent, r.chain)
		})
	}
}

func liveActionsURL(host, cases, key string) (*url.URL, error) {
	if host == "" && cases == "" {
		return nil, nil
	}
	if host == "" {
		return nil, errors.New("set ACTIONS_API_URL for live requests")
	}
	base, err := url.Parse(host)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid ACTIONS_API_URL")
	}
	hostname := base.Hostname()
	ip, _ := netip.ParseAddr(hostname)
	loopback := strings.ToLower(hostname) == "localhost" || (ip.Is4() && ip.IsLoopback()) || ip == netip.IPv6Loopback()
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return nil, errors.New("ACTIONS_API_URL must use HTTPS or local loopback HTTP")
	}
	if key == "" && !loopback {
		return nil, errors.New("set ACTIONS_API_KEY for live requests")
	}
	if cases == "" {
		return nil, errors.New("set ACTIONS_API_CASES to a readable JSON array of intents")
	}
	return base, nil
}

func TestLiveActionsConfiguration(t *testing.T) {
	const host = "https://example.test"
	const cases = "cases.json"
	const key = "test-key"
	for _, tc := range []struct {
		name, host, cases, key string
		enabled                bool
		missing                string
	}{
		{name: "unset"},
		{name: "key alone does not opt in", key: key},
		{name: "cases without URL", cases: cases, key: key, missing: "ACTIONS_API_URL"},
		{name: "cases without key", host: host, cases: cases, missing: "ACTIONS_API_KEY"},
		{name: "keyless localhost", host: "http://localhost:8080", cases: cases, enabled: true},
		{name: "keyless IPv4 loopback", host: "http://127.42.0.7:8080", cases: cases, enabled: true},
		{name: "keyless IPv6 loopback", host: "https://[::1]:8080", cases: cases, enabled: true},
		{name: "localhost suffix", host: "https://localhost.example.test", cases: cases, missing: "ACTIONS_API_KEY"},
		{name: "outside IPv4 loopback", host: "https://128.0.0.1", cases: cases, missing: "ACTIONS_API_KEY"},
		{name: "IPv4-mapped IPv6", host: "https://[::ffff:127.0.0.1]", cases: cases, missing: "ACTIONS_API_KEY"},
		{name: "non-loopback HTTP", host: "http://128.0.0.1", cases: cases, key: key, missing: "must use HTTPS"},
		{name: "URL without cases", host: host, key: key, missing: "ACTIONS_API_CASES"},
		{name: "complete", host: host, cases: cases, key: key, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := liveActionsURL(tc.host, tc.cases, tc.key)
			enabled := base != nil
			if enabled != tc.enabled {
				t.Errorf("enabled = %t, want %t", enabled, tc.enabled)
			}
			if tc.missing == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.missing) {
				t.Errorf("error = %v, want a message naming %s", err, tc.missing)
			}
		})
	}
}

func checkLive(t *testing.T, client *http.Client, base, key string, intent Intent, chain uint64) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+intent.Path, bytes.NewReader(intent.Body))
	if err != nil {
		t.Errorf("chain %d: cannot construct request", chain)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Errorf("chain %d: API unavailable (transport failure)", chain)
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		t.Errorf("chain %d: authentication failure (HTTP %d)", chain, res.StatusCode)
		return true
	}
	if res.StatusCode >= 500 {
		t.Errorf("chain %d: API unavailable (HTTP %d)", chain, res.StatusCode)
		return false
	}
	if res.StatusCode >= 300 {
		t.Errorf("chain %d: request rejected (HTTP %d); check account or market prerequisites", chain, res.StatusCode)
		return false
	}
	var result struct {
		Data struct {
			Status string
			Calls  []Call
		}
	}
	if json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&result) != nil {
		t.Errorf("chain %d: invalid API response", chain)
	} else {
		switch result.Data.Status {
		case "prepared":
			if Verify(intent, result.Data.Calls) {
				t.Logf("chain %d: verified true", chain)
			} else {
				t.Errorf("chain %d: verifier returned false on prepared response", chain)
			}
		case "simulation_failed":
			t.Errorf("chain %d: simulation failed; check account or position prerequisites", chain)
		case "signature_required":
			t.Errorf("chain %d: API requires a signature continuation", chain)
		default:
			t.Errorf("chain %d: unsupported API response status", chain)
		}
	}
	return false
}
