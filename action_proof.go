package calldataverifier

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
)

// ActionProofInput contains the exact HTTP exchange, independently trusted JWKS
// JSON, and the current Unix time in seconds supplied by the caller.
type ActionProofInput struct {
	Header   string
	JWKS     json.RawMessage
	Request  ActionProofRequest
	Response ActionProofResponse
	Issuer   *string
	Now      *float64
}

// ActionProofRequest describes the HTTP request as sent.
type ActionProofRequest struct {
	Method      string
	Path        string
	ContentType string
	Body        []byte
}

// ActionProofResponse describes the HTTP response and its decompressed body,
// before JSON parsing or reserialization.
type ActionProofResponse struct {
	Status      int
	ContentType string
	RequestID   string
	Body        []byte
}

const (
	proofAlgorithm = "ES256"
	proofType      = "morpho-action-proof+jwt"
	proofIssuer    = "https://api.morpho.org/v0/actions"
	proofAudience  = "morpho-actions-client"
)

func proofError(field string, cause error) error {
	return &CalldataProofError{detail(field, nil, cause)}
}

func proofObject(data []byte, field string) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, proofError("proof", errors.New("invalid UTF-8"))
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, proofError("proof", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, proofError("proof", errors.New("trailing JSON data"))
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, proofError(field, nil)
	}
	return obj, nil
}

func proofBase64Alphabet(encoded string) bool {
	if encoded == "" {
		return false
	}
	for _, c := range encoded {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func decodeProofPart(encoded string) ([]byte, error) {
	if !proofBase64Alphabet(encoded) {
		return nil, proofError("proof.encoding", nil)
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, proofError("proof", err)
	}
	return data, nil
}

func decodeProofObject(part, field string) (map[string]any, error) {
	data, err := decodeProofPart(part)
	if err != nil {
		return nil, err
	}
	return proofObject(data, field)
}

func proofString(value any, expected string) bool {
	s, ok := value.(string)
	return ok && s == expected
}

func optionalProofString(object map[string]any, key, expected string) bool {
	value, present := object[key]
	return !present || proofString(value, expected)
}

func proofSingleString(value any, expected string) bool {
	items, ok := value.([]any)
	return ok && len(items) == 1 && proofString(items[0], expected)
}

func proofPublicKey(jwks json.RawMessage, kid string) (*ecdsa.PublicKey, error) {
	set, err := proofObject(jwks, "proof.jwks")
	if err != nil {
		return nil, proofError("proof.jwks", nil)
	}
	keys, _ := set["keys"].([]any)
	var key map[string]any
	for _, entry := range keys {
		candidate, ok := entry.(map[string]any)
		if ok && proofString(candidate["kid"], kid) {
			if key != nil {
				return nil, proofError("proof.jwks", nil)
			}
			key = candidate
		}
	}
	for field := range key {
		switch field {
		case "kty", "crv", "x", "y", "alg", "kid", "key_ops", "use":
		default:
			return nil, proofError("proof.jwks", nil)
		}
	}
	x, _ := key["x"].(string)
	y, _ := key["y"].(string)
	keyOps, hasKeyOps := key["key_ops"]
	if !proofString(key["kty"], "EC") || !proofString(key["crv"], "P-256") ||
		len(x) != 43 || len(y) != 43 || !proofBase64Alphabet(x) || !proofBase64Alphabet(y) ||
		!optionalProofString(key, "alg", proofAlgorithm) || !optionalProofString(key, "use", "sig") ||
		(hasKeyOps && !proofSingleString(keyOps, "verify")) {
		return nil, proofError("proof.jwks", nil)
	}
	xb, err := decodeProofPart(x)
	if err != nil {
		return nil, err
	}
	yb, err := decodeProofPart(y)
	if err != nil {
		return nil, err
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}, nil
}

func proofNumber(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	f, _ := n.Float64()
	return f, true
}

func proofSafeInteger(n float64) bool {
	return math.Abs(n) <= 1<<53-1 && n == math.Trunc(n)
}

func proofClaims(claims map[string]any, issuer string, now float64) error {
	milliseconds := now * 1000
	if math.Abs(milliseconds) > 8_640_000_000_000_000 {
		return proofError("proof.claims", nil)
	}
	now = math.Floor(math.Trunc(milliseconds) / 1000)
	audience := proofString(claims["aud"], proofAudience)
	if list, ok := claims["aud"].([]any); ok {
		for _, item := range list {
			audience = audience || proofString(item, proofAudience)
		}
	}
	iat, hasIAT := proofNumber(claims["iat"])
	exp, hasEXP := proofNumber(claims["exp"])
	const tolerance = 5
	if !proofString(claims["iss"], issuer) || !proofString(claims["sub"], "action-response") || !audience ||
		!hasIAT || !proofSafeInteger(iat) || !hasEXP || !proofSafeInteger(exp) ||
		exp <= iat || exp-iat > 300 || iat > now+tolerance ||
		now-iat > 60+tolerance || exp <= now-tolerance {
		return proofError("proof.claims", nil)
	}
	if raw, present := claims["nbf"]; present {
		nbf, ok := proofNumber(raw)
		if !ok || math.IsInf(nbf, 0) || nbf > now+tolerance {
			return proofError("proof.claims", nil)
		}
	}
	return nil
}

func proofDecision(data map[string]any) string {
	simulation, _ := data["simulation"].(map[string]any)
	switch data["status"] {
	case "signature_required":
		return "signature_required"
	case "prepared":
		if proofString(simulation["status"], "success") {
			return "approved"
		}
	case "simulation_unavailable":
		if proofString(simulation["status"], "unavailable") {
			return "unsimulated"
		}
	}
	return "rejected"
}

func proofHash(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sameProofField(a, b map[string]any, field string) bool {
	left, leftPresent := a[field]
	right, rightPresent := b[field]
	if leftPresent != rightPresent {
		return false
	}
	switch value := left.(type) {
	case nil:
		return right == nil
	case string:
		return proofString(right, value)
	case bool:
		same, ok := right.(bool)
		return ok && same == value
	case json.Number:
		x, _ := value.Float64()
		y, ok := proofNumber(right)
		return ok && x == y
	default:
		return false
	}
}

func verifyProofExchange(proof ActionProofInput) (map[string]any, string, error) {
	if proof.Now == nil || math.IsNaN(*proof.Now) || math.IsInf(*proof.Now, 0) {
		return nil, "", proofError("proof.now", nil)
	}
	parts := strings.SplitN(proof.Header, ".", 4)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, "", proofError("proof.header", nil)
	}
	header, err := decodeProofObject(parts[0], "proof.header")
	if err != nil {
		return nil, "", err
	}
	kid, _ := header["kid"].(string)
	b64, hasB64 := header["b64"]
	crit, hasCrit := header["crit"]
	if !proofString(header["alg"], proofAlgorithm) || !proofString(header["typ"], proofType) || kid == "" ||
		(hasB64 && b64 != true) || (hasCrit && (!proofSingleString(crit, "b64") || b64 != true)) {
		return nil, "", proofError("proof.header", nil)
	}
	signature, err := decodeProofPart(parts[2])
	if err != nil {
		return nil, "", err
	}
	if len(signature) != 64 {
		return nil, "", proofError("proof.signature", nil)
	}
	key, err := proofPublicKey(proof.JWKS, kid)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return nil, "", proofError("proof.signature", nil)
	}
	claims, err := decodeProofObject(parts[1], "proof.claims")
	if err != nil {
		return nil, "", err
	}
	issuer := proofIssuer
	if proof.Issuer != nil {
		issuer = *proof.Issuer
	}
	if err := proofClaims(claims, issuer, *proof.Now); err != nil {
		return nil, "", err
	}
	return checkProofExchange(proof.Request, proof.Response, claims)
}

func checkProofExchange(request ActionProofRequest, response ActionProofResponse, claims map[string]any) (map[string]any, string, error) {
	signedRequest, reqOK := claims["request"].(map[string]any)
	signedResponse, resOK := claims["response"].(map[string]any)
	status, statusOK := proofNumber(signedResponse["status"])
	if request.Method != "POST" || !strings.HasPrefix(request.Path, "/v0/actions/") ||
		len(request.Body) == 0 || response.Body == nil ||
		!proofString(claims["request_id"], response.RequestID) || !reqOK ||
		!proofString(signedRequest["method"], request.Method) ||
		!proofString(signedRequest["target"], request.Path) ||
		!proofString(signedRequest["content_type"], request.ContentType) ||
		!resOK || !statusOK || status != float64(response.Status) ||
		!proofString(signedResponse["content_type"], response.ContentType) {
		return nil, "", proofError("proof.exchange", nil)
	}
	requestBody, responseBody := bytes.Clone(request.Body), bytes.Clone(response.Body)
	if !proofString(signedRequest["body_sha256"], proofHash(requestBody)) ||
		!proofString(signedResponse["body_sha256"], proofHash(responseBody)) {
		return nil, "", proofError("proof.exchange", nil)
	}
	object, err := proofObject(responseBody, "proof.response")
	if err != nil {
		return nil, "", err
	}
	data, ok := object["data"].(map[string]any)
	if !ok {
		return nil, "", proofError("proof.response", nil)
	}
	decision := proofDecision(data)
	if !sameProofField(claims, data, "action_id") || !sameProofField(claims, data, "action") ||
		!proofString(claims["decision"], decision) {
		return nil, "", proofError("proof.response", nil)
	}
	return data, decision, nil
}

func authenticateCalls(proof ActionProofInput, calls []CalldataCall, chainID uint64) ([]CalldataCall, error) {
	data, decision, err := verifyProofExchange(proof)
	if err != nil {
		return nil, err
	}
	if decision != "approved" {
		return nil, proofError("proof.decision", nil)
	}
	chain, ok := proofNumber(data["chain_id"])
	if !ok || !proofSafeInteger(chain) || chainID > 1<<53-1 || chain != float64(chainID) {
		return nil, proofError("proof.chain_id", nil)
	}
	signed, ok := data["calls"].([]any)
	if !ok || calls == nil || len(signed) != len(calls) {
		return nil, proofError("proof.calls", nil)
	}
	authenticated := make([]CalldataCall, len(calls))
	for i, raw := range signed {
		call, ok := raw.(map[string]any)
		want := calls[i]
		if !ok || !proofString(call["id"], want.ID) || !proofString(call["kind"], want.Kind) ||
			!proofString(call["to"], want.To) || !proofString(call["data"], want.Data) || !proofString(call["value"], want.Value) {
			return nil, proofError("proof.calls", nil)
		}
		authenticated[i] = want
	}
	return authenticated, nil
}
