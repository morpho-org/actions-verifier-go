package verifier

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveActions(t *testing.T) {
	host := os.Getenv("ACTIONS_API_URL")
	if host == "" {
		t.Skip("set ACTIONS_API_URL to enable live requests")
	}
	base, err := url.Parse(host)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		t.Fatal("invalid ACTIONS_API_URL")
	}
	localHTTP := base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1")
	if base.Scheme != "https" && !localHTTP {
		t.Fatal("ACTIONS_API_URL must use HTTPS or local loopback HTTP")
	}
	input, err := os.ReadFile(os.Getenv("ACTIONS_API_CASES"))
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
			denied = checkLive(t, client, strings.TrimRight(base.String(), "/"), os.Getenv("ACTIONS_API_KEY"), intent, r.chain)
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
