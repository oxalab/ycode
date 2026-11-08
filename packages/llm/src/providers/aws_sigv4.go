package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// awsSignV4 signs an HTTP request with AWS Signature Version 4.
// It modifies the request headers in place to include the Authorization header.
func awsSignV4(req *http.Request, accessKey, secretKey, sessionToken, region, service string, body []byte) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	// Set required headers
	req.Header.Set("X-Amz-Date", amzDate)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	// Compute content hash
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Build canonical request
	canonicalURI := canonicalPath(req.URL)
	canonicalQueryString := canonicalQuery(req.URL)
	canonicalHeaders, signedHeaders := buildCanonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		"",
		signedHeaders,
	}, "\n")

	// Build string to sign
	credentialScope := fmt.Sprintf("%s/%s/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// Calculate signing key
	signingKey := deriveSigningKey(secretKey, dateStamp, region, service)

	// Calculate signature
	signature := hmacHex(signingKey, []byte(stringToSign))

	// Build authorization header
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authorization)
}

// sha256Hex returns the hex-encoded SHA-256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hmacSHA256 computes HMAC-SHA256 and returns raw bytes.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// hmacHex computes HMAC-SHA256 and returns hex-encoded string.
func hmacHex(key []byte, data []byte) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

// deriveSigningKey derives the AWS Sig V4 signing key.
func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// canonicalPath returns the URI-encoded path.
func canonicalPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return path
}

// canonicalQuery returns the sorted, canonical query string.
func canonicalQuery(u *url.URL) string {
	return u.Query().Encode() // net/url sorts keys
}

// buildCanonicalHeaders builds the canonical headers string and signed headers list.
// The Host header is a separate http.Request field in Go, not part of req.Header,
// so it must be merged in explicitly — SigV4 requires `host` to be signed.
func buildCanonicalHeaders(req *http.Request) (string, string) {
	// Collect and sort header names
	headers := make(map[string][]string)
	for k, vv := range req.Header {
		lk := strings.ToLower(k)
		headers[lk] = vv
	}

	// Merge in the host (Go stores it outside req.Header).
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers["host"] = []string{host}

	// Sort header names
	sorted := make([]string, 0, len(headers))
	for k := range headers {
		sorted = append(sorted, k)
	}
	// Simple sort
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Build canonical headers
	var chParts []string
	for _, k := range sorted {
		values := strings.Join(headers[k], ",")
		chParts = append(chParts, k+":"+strings.TrimSpace(values))
	}
	canonicalHeaders := strings.Join(chParts, "\n") + "\n"
	signedHeaders := strings.Join(sorted, ";")

	return canonicalHeaders, signedHeaders
}
