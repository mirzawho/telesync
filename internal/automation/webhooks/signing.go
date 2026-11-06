package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Standard Webhooks headers. A receiver verifies the body against these three
// values exactly as it would for any other Standard Webhooks sender.
const (
	// IDHeader names the logical delivery. It is stable for the lifetime of one
	// delivery and identical on every retry, so a receiver can deduplicate.
	IDHeader = "webhook-id"
	// TimestampHeader carries the signature's timestamp in unix seconds. A
	// retry re-signs with a fresh timestamp rather than replaying an old one.
	TimestampHeader = "webhook-timestamp"
	// SignatureHeader carries one or more "v1,<base64>" signatures.
	SignatureHeader = "webhook-signature"
)

const (
	// signatureVersion is the only signature version this build emits.
	signatureVersion = "v1"
	// signatureSeparator joins the signed fields.
	signatureSeparator = "."
	// secretPrefix marks a secret whose key material is base64 encoded.
	secretPrefix = "whsec_"
)

// ErrBadSignature means the signature does not match the request as received.
var ErrBadSignature = errors.New("webhooks: the signature does not match")

// SigningKey derives the HMAC key from a stored secret.
//
// A secret written in the Standard Webhooks form "whsec_<base64>" is decoded to
// the bytes it encodes, so a secret issued by another implementation verifies
// here and ours verifies there. Anything else — a passphrase, a hex string the
// operator chose — is used as its literal UTF-8 bytes, because silently
// reinterpreting an operator's own secret would make it stop matching what they
// configured the receiver with.
func SigningKey(secret string) ([]byte, error) {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return nil, errors.New("webhooks: the secret is empty")
	}
	if !strings.HasPrefix(trimmed, secretPrefix) {
		return []byte(trimmed), nil
	}
	encoded := strings.TrimPrefix(trimmed, secretPrefix)
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("webhooks: the whsec_ secret is not valid base64")
	}
	if len(key) == 0 {
		return nil, errors.New("webhooks: the whsec_ secret is empty")
	}
	return key, nil
}

// Signature is one Standard Webhooks signature over exactly the bytes that were
// transmitted. Nothing re-serializes the payload after this is computed: what
// was signed is what is sent.
type Signature struct {
	// ID is the stable logical delivery id.
	ID string
	// Timestamp is the moment the signature was made, at second resolution.
	Timestamp time.Time
	// Value is the full header value, "v1,<base64>".
	Value string
}

// Sign signs one request body.
//
// The signed message is webhook-id "." webhook-timestamp "." body, with the
// timestamp in unix seconds, exactly as the Standard Webhooks scheme requires.
// A retry keeps deliveryID and passes a later at, which is what stops a replayed
// attempt from being accepted as fresh.
func Sign(secret, deliveryID string, at time.Time, body []byte) (Signature, error) {
	if strings.TrimSpace(deliveryID) == "" {
		return Signature{}, errors.New("webhooks: the delivery id is empty")
	}
	key, err := SigningKey(secret)
	if err != nil {
		return Signature{}, err
	}
	ts := strconv.FormatInt(at.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(deliveryID + signatureSeparator + ts + signatureSeparator))
	mac.Write(body)
	sum := mac.Sum(nil)
	return Signature{
		ID:        deliveryID,
		Timestamp: at.UTC(),
		Value:     signatureVersion + "," + base64.StdEncoding.EncodeToString(sum),
	}, nil
}

// Verify reports whether signature matches the body as received.
//
// It exists for tests and for the documentation that shows a receiver how to
// check a delivery. No route in this application accepts a signature, so there
// is nothing for an attacker to aim it at; it is kept here so the scheme is
// verified against itself rather than described and hoped for.
//
// Comparison is constant time across every candidate signature, so neither the
// length of a candidate nor the position of a match is observable from timing.
func Verify(secret, deliveryID string, at time.Time, body []byte, signature string) error {
	key, err := SigningKey(secret)
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(at.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(deliveryID + signatureSeparator + ts + signatureSeparator))
	mac.Write(body)
	want := mac.Sum(nil)

	matched := 0
	seen := 0
	for _, candidate := range strings.Fields(signature) {
		seen++
		version, encoded, found := strings.Cut(candidate, ",")
		if !found || version != signatureVersion {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		matched |= subtle.ConstantTimeCompare(got, want)
	}
	if seen == 0 || matched == 0 {
		return ErrBadSignature
	}
	return nil
}

// Headers returns the three headers a request must carry. The caller sets them
// on the request it actually sends; they are never recomputed afterwards.
func (s Signature) Headers() http.Header {
	h := make(http.Header, 3)
	h.Set(IDHeader, s.ID)
	h.Set(TimestampHeader, strconv.FormatInt(s.Timestamp.UTC().Unix(), 10))
	h.Set(SignatureHeader, s.Value)
	return h
}
