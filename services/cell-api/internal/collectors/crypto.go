// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package collectors

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/secretcrypto"
)

// ErrNoKey means the cell has no collector configuration key, so
// configurations are neither stored nor shown.
var ErrNoKey = errors.New("collectors: SLUICIO_COLLECTOR_CONFIG_KEY is not set")

// Keys are the two keys derived from the cell's collector configuration
// key. The base key lives outside the database (an environment variable,
// a Kubernetes Secret); unlike the MFA key there is deliberately no
// generated fallback stored in cell_settings, because a key kept beside
// the ciphertext protects nothing against a database reader.
type Keys struct {
	enc []byte
	mac []byte
}

// ParseKeys reads the base64-encoded 32-byte base key and derives the
// encryption and hashing keys from it, so neither is ever used for the
// other's purpose.
func ParseKeys(b64 string) (*Keys, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("collectors: SLUICIO_COLLECTOR_CONFIG_KEY must be 32 bytes, base64-encoded")
	}
	enc, err := hkdf.Key(sha256.New, raw, nil, "sluicio collector config encryption v1", 32)
	if err != nil {
		return nil, err
	}
	mac, err := hkdf.Key(sha256.New, raw, nil, "sluicio collector config hash v1", 32)
	if err != nil {
		return nil, err
	}
	return &Keys{enc: enc, mac: mac}, nil
}

// ConfigFile is one file of an agent's configuration. OpAMP reports a map
// of files; the collector and supervisor send a single unnamed one.
type ConfigFile struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
}

// canonical serialises files in name order, so the same configuration
// always hashes the same however the agent ordered its map.
func canonical(files []ConfigFile) []byte {
	sorted := append([]ConfigFile{}, files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	b, _ := json.Marshal(sorted)
	return b
}

// Hash is the change-detection fingerprint stored in the clear: an HMAC,
// so it says "this changed" without letting anyone holding the database
// test a guess at what the configuration contains.
func (k *Keys) Hash(files []ConfigFile) string {
	m := hmac.New(sha256.New, k.mac)
	m.Write(canonical(files))
	return hex.EncodeToString(m.Sum(nil))
}

func aad(collectorID uuid.UUID, hash string) []byte {
	return []byte("collector-config:v1:" + collectorID.String() + ":" + hash)
}

// Seal encrypts the whole configuration, bound to its collector and its
// hash: ciphertext copied to another collector's row, or paired with
// another hash, does not open.
func (k *Keys) Seal(collectorID uuid.UUID, files []ConfigFile) (hash, sealed string, err error) {
	hash = k.Hash(files)
	sealed, err = secretcrypto.EncryptBound(k.enc, string(canonical(files)), aad(collectorID, hash))
	if err != nil {
		return "", "", err
	}
	return hash, sealed, nil
}

// Open reverses Seal.
func (k *Keys) Open(collectorID uuid.UUID, hash, sealed string) ([]ConfigFile, error) {
	if !secretcrypto.IsEncrypted(sealed) {
		// Every row is written by Seal. A plaintext one was put there by
		// something else and is not shown as if it were ours.
		return nil, errors.New("collectors: stored configuration is not encrypted")
	}
	plain, err := secretcrypto.DecryptBound(k.enc, sealed, aad(collectorID, hash))
	if err != nil {
		return nil, fmt.Errorf("collectors: open configuration: %w", err)
	}
	var files []ConfigFile
	if err := json.Unmarshal([]byte(plain), &files); err != nil {
		return nil, fmt.Errorf("collectors: decode configuration: %w", err)
	}
	return files, nil
}
