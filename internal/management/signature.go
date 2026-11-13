package management

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
)

const ControlDomain = "topophany.control.v1"

type Signature struct {
	KeyID string `json:"key_id"`
	Value string `json:"signature"`
}
type Envelope struct {
	Payload    string      `json:"payload_b64"`
	Signatures []Signature `json:"signatures"`
}
type Key struct {
	ID     string   `json:"id"`
	Public string   `json:"public_key"`
	Roles  []string `json:"roles"`
}
type Trust struct {
	RoleThresholds map[string]int `json:"role_thresholds,omitempty"`
	Schema         int            `json:"schema"`
	ID             string         `json:"id"`
	Epoch          uint64         `json:"epoch"`
	Threshold      int            `json:"threshold"`
	Expires        time.Time      `json:"expires_at"`
	Keys           []Key          `json:"keys"`
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func HashOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == hex.EncodeToString(b)
}
func (t Trust) Validate(now time.Time) error {
	if t.Schema != 1 || !config.ID(t.ID) || t.Epoch == 0 || t.Threshold < 1 || t.Threshold > 4 || len(t.Keys) < t.Threshold || len(t.Keys) > 16 || !t.Expires.After(now) {
		return errors.New("invalid_or_expired_trust")
	}
	seen := map[string]bool{}
	public := map[string]bool{}
	for _, k := range t.Keys {
		b, e := base64.RawURLEncoding.DecodeString(k.Public)
		if e != nil || len(b) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(b) != k.Public || !config.ID(k.ID) || seen[k.ID] || public[k.Public] || len(k.Roles) == 0 || len(k.Roles) > 5 {
			return errors.New("invalid_trust_key")
		}
		seen[k.ID] = true
		public[k.Public] = true
		for _, role := range k.Roles {
			switch role {
			case "config", "update", "release", "catalog", "root":
			default:
				return errors.New("invalid_key_role")
			}
		}
	}
	for role, threshold := range t.RoleThresholds {
		if threshold < 1 || threshold > 4 {
			return errors.New("invalid_role_threshold")
		}
		count := 0
		for _, key := range t.Keys {
			for _, r := range key.Roles {
				if r == role {
					count++
					break
				}
			}
		}
		if count < threshold {
			return errors.New("unreachable_role_threshold")
		}
	}
	return nil
}
func (t Trust) ThresholdFor(role string) int {
	if threshold, ok := t.RoleThresholds[role]; ok {
		return threshold
	}
	return t.Threshold
}
func Sign(domain string, payload []byte, keyID string, key ed25519.PrivateKey) (Envelope, error) {
	if len(key) != ed25519.PrivateKeySize || !config.ID(keyID) || len(payload) > 256<<10 {
		return Envelope{}, errors.New("invalid_signing_input")
	}
	sig := ed25519.Sign(key, append([]byte(domain+"\x00"), payload...))
	return Envelope{base64.RawURLEncoding.EncodeToString(payload), []Signature{{keyID, base64.RawURLEncoding.EncodeToString(sig)}}}, nil
}
func Verify(domain, role string, e Envelope, t Trust, now time.Time, max int) ([]byte, error) {
	if err := t.Validate(now); err != nil {
		return nil, err
	}
	if len(e.Payload) > base64.RawURLEncoding.EncodedLen(max) || len(e.Signatures) < t.ThresholdFor(role) || len(e.Signatures) > 4 {
		return nil, errors.New("invalid_signature_envelope")
	}
	b, err := base64.RawURLEncoding.DecodeString(e.Payload)
	if err != nil || len(b) > max || base64.RawURLEncoding.EncodeToString(b) != e.Payload {
		return nil, errors.New("invalid_signed_payload")
	}
	keys := map[string]ed25519.PublicKey{}
	for _, k := range t.Keys {
		for _, r := range k.Roles {
			if r == role {
				v, _ := base64.RawURLEncoding.DecodeString(k.Public)
				keys[k.ID] = v
			}
		}
	}
	seen := map[string]bool{}
	count := 0
	message := append([]byte(domain+"\x00"), b...)
	for _, s := range e.Signatures {
		if seen[s.KeyID] {
			return nil, errors.New("duplicate_signature_key")
		}
		seen[s.KeyID] = true
		sig, err := base64.RawURLEncoding.DecodeString(s.Value)
		key := keys[s.KeyID]
		if err == nil && base64.RawURLEncoding.EncodeToString(sig) == s.Value && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, message, sig) {
			count++
		}
	}
	if count < t.ThresholdFor(role) {
		return nil, errors.New("signature_verification_failed")
	}
	return b, nil
}
func LoadTrust(path string, system bool, now time.Time) (Trust, error) {
	b, e := ReadLocal(path, 64<<10, system, false)
	if e != nil {
		return Trust{}, e
	}
	var t Trust
	if e = Decode(b, &t, 64<<10); e != nil {
		return t, e
	}
	return t, t.Validate(now)
}
func Marshal(v any) []byte { b, _ := json.Marshal(v); return b }
