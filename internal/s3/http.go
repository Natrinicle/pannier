package s3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Options configure a destination object store. file:// stays a local
// berth; anything else is path-style S3 (MinIO, B2, AWS).
type Options struct {
	Endpoint   string
	Bucket     string
	Prefix     string
	Region     string
	AccessKey  string
	SecretKey  string
	DisableTLS bool
}

// OpenWith opens file:// or an HTTP S3 endpoint. Locked Packs only needs
// List and Put; this client does not Head or Delete.
func OpenWith(opt Options) (ObjectStore, error) {
	ep := strings.TrimSpace(opt.Endpoint)
	if strings.HasPrefix(ep, "file://") {
		return Open(ep, opt.Bucket, opt.Prefix)
	}
	if opt.Bucket == "" {
		return nil, fmt.Errorf("object store: empty bucket")
	}
	base, err := s3BaseURL(ep, opt.DisableTLS)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(opt.Region)
	if region == "" {
		region = "us-east-1"
	}
	prefix := strings.Trim(strings.ReplaceAll(opt.Prefix, "\\", "/"), "/")
	if prefix != "" {
		prefix += "/"
	}
	return &HTTPStore{
		base:      base,
		bucket:    opt.Bucket,
		prefix:    prefix,
		region:    region,
		accessKey: opt.AccessKey,
		secretKey: opt.SecretKey,
		client:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func s3BaseURL(endpoint string, disableTLS bool) (*url.URL, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return nil, fmt.Errorf("object store: empty endpoint")
	}
	if !strings.Contains(ep, "://") {
		scheme := "https"
		if disableTLS {
			scheme = "http"
		}
		ep = scheme + "://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil {
		return nil, fmt.Errorf("object store endpoint: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("object store endpoint %q: missing host", endpoint)
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

// HTTPStore is path-style S3: /bucket/key on the given host.
type HTTPStore struct {
	base      *url.URL
	bucket    string
	prefix    string
	region    string
	accessKey string
	secretKey string
	client    *http.Client
	now       func() time.Time
}

func (h *HTTPStore) List(ctx context.Context, prefix string) ([]string, error) {
	want := h.prefix + strings.TrimPrefix(prefix, "/")
	var keys []string
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		if want != "" {
			q.Set("prefix", want)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		body, err := h.roundTrip(ctx, http.MethodGet, "/"+h.bucket, q, nil, 0, emptyPayloadHash)
		if err != nil {
			return nil, err
		}
		var res listBucketResult
		if err := xml.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("s3 list: %w", err)
		}
		for _, c := range res.Contents {
			key := c.Key
			if h.prefix != "" {
				key = strings.TrimPrefix(key, h.prefix)
			}
			if key != "" {
				keys = append(keys, key)
			}
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	return keys, nil
}

func (h *HTTPStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if key == "" || strings.Contains(key, "..") {
		return fmt.Errorf("refusing object key %q", key)
	}
	full := h.prefix + strings.TrimPrefix(key, "/")
	payload, sum, n, err := readPayload(r, size)
	if err != nil {
		return err
	}
	path := "/" + h.bucket + "/" + full
	_, err = h.roundTrip(ctx, http.MethodPut, path, nil, payload, n, sum)
	return err
}

func readPayload(r io.Reader, size int64) ([]byte, string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, "", 0, err
	}
	n := int64(len(b))
	if size > 0 && n != size {
		return nil, "", 0, fmt.Errorf("put: read %d, expected %d", n, size)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), n, nil
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type listBucketResult struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	Contents []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (h *HTTPStore) roundTrip(ctx context.Context, method, path string, query url.Values, body []byte, size int64, payloadHash string) ([]byte, error) {
	u := *h.base
	u.Path = path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}
	if size > 0 || method == http.MethodPut {
		req.ContentLength = size
	}
	if err := h.sign(req, payloadHash); err != nil {
		return nil, err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return nil, fmt.Errorf("s3 %s %s: HTTP %d %s", method, path, res.StatusCode, msg)
	}
	return out, nil
}

func (h *HTTPStore) sign(req *http.Request, payloadHash string) error {
	if h.accessKey == "" || h.secretKey == "" {
		return fmt.Errorf("s3: missing ingest credentials")
	}
	now := time.Now().UTC()
	if h.now != nil {
		now = h.now().UTC()
	}
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	host := req.URL.Host
	req.Header.Set("Host", host)
	req.Host = host

	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalQuery := encodeCanonicalQuery(req.URL.Query())
	canonicalPath := req.URL.EscapedPath()
	if canonicalPath == "" {
		canonicalPath = "/"
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := dateStamp + "/" + h.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	sig := hex.EncodeToString(hmacSHA256(deriveSigningKey(h.secretKey, dateStamp, h.region, "s3"), stringToSign))
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+h.accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+sig)
	return nil
}

func encodeCanonicalQuery(v url.Values) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), v[k]...)
		sort.Strings(vs)
		for _, val := range vs {
			parts = append(parts, awsEncode(k)+"="+awsEncode(val))
		}
	}
	return strings.Join(parts, "&")
}

func awsEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(s))
	return m.Sum(nil)
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
