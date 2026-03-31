// Package license provides offline Ed25519-based license key validation
// for Stockyard tools. License keys are issued by the Stockyard license
// server and validated locally with no network call required.
//
// Key format: stockyard_<base64url(payload_json)>.<base64url(signature)>
// Payload:    {"p":"corral","t":"pro","e":1780000000,"c":"cust_xxx","i":1743000000}
//   p = product slug
//   t = tier ("pro")
//   e = expires_at unix timestamp (0 = never expires)
//   c = customer identifier
//   i = issued_at unix timestamp
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// publicKeyHex is the Ed25519 public key used to verify all Stockyard licenses.
// The matching private key is held by Stockyard and never embedded in binaries.
const publicKeyHex = "3af8f9593b3331c27994f1eeacf111c727ff6015016b0af44ed3ca6934d40b13"

// Tier represents a product license tier.
type Tier string

const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Info holds the validated contents of a license key.
type Info struct {
	Product    string
	Tier       Tier
	CustomerID string
	IssuedAt   time.Time
	ExpiresAt  time.Time // zero value = never expires
	NeverExp   bool
}

// IsPro returns true if the license grants Pro-tier access.
func (i *Info) IsPro() bool {
	if i == nil {
		return false
	}
	return i.Tier == TierPro
}

// payload is the JSON structure embedded in each license key.
type payload struct {
	Product    string `json:"p"`
	Tier       string `json:"t"`
	ExpiresAt  int64  `json:"e"` // unix; 0 = never
	CustomerID string `json:"c"`
	IssuedAt   int64  `json:"i"`
}

// Validate parses and verifies a license key string for the given product.
// Returns nil, nil when keyStr is empty (Free tier, no error).
// Returns an error if the key is malformed, has an invalid signature,
// is expired, or is issued for a different product.
func Validate(keyStr, product string) (*Info, error) {
	keyStr = strings.TrimSpace(keyStr)
	if keyStr == "" {
		return nil, nil // free tier
	}

	// Strip optional "stockyard_" prefix
	keyStr = strings.TrimPrefix(keyStr, "stockyard_")

	parts := strings.SplitN(keyStr, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("license: malformed key (expected payload.signature)")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("license: invalid payload encoding: %w", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("license: invalid signature encoding: %w", err)
	}

	// Verify signature
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("license: internal error decoding public key: %w", err)
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)
	if !ed25519.Verify(pubKey, payloadBytes, sigBytes) {
		return nil, errors.New("license: invalid signature — key may be tampered or forged")
	}

	// Decode payload
	var p payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return nil, fmt.Errorf("license: invalid payload JSON: %w", err)
	}

	// Check product match
	if p.Product != product {
		return nil, fmt.Errorf("license: key is for %q, not %q", p.Product, product)
	}

	// Check expiry
	neverExp := p.ExpiresAt == 0
	var expiresAt time.Time
	if !neverExp {
		expiresAt = time.Unix(p.ExpiresAt, 0)
		if time.Now().After(expiresAt) {
			return nil, fmt.Errorf("license: key expired on %s", expiresAt.Format("2006-01-02"))
		}
	}

	tier := TierFree
	if p.Tier == "pro" {
		tier = TierPro
	}

	return &Info{
		Product:    p.Product,
		Tier:       tier,
		CustomerID: p.CustomerID,
		IssuedAt:   time.Unix(p.IssuedAt, 0),
		ExpiresAt:  expiresAt,
		NeverExp:   neverExp,
	}, nil
}
