package coin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const successCode = "SUCCESS"

const maxResponseBodyBytes = 4 * 1024 * 1024

// ClientOptions defines SDK client construction options.
type ClientOptions struct {
	BaseURL        string
	MerchantNo     string
	MerchantSecret string
	HTTPClient     *http.Client
	Timeout        time.Duration
	Now            func() time.Time
	NonceGenerator func() string
	UserAgent      string
}

type envelope struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	RequestID string          `json:"request_id"`
	Data      json.RawMessage `json:"data"`
}

// Client is the merchant SDK root entry.
type Client struct {
	baseURL        *url.URL
	merchantNo     string
	merchantSecret string
	httpClient     *http.Client
	now            func() time.Time
	nonce          func() string
	userAgent      string

	Merchant     *MerchantAPI
	Transactions *TransactionsAPI
}

// NewClient creates a merchant SDK client.
func NewClient(opts ClientOptions) (*Client, error) {
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("base_url must include scheme and host")
	}

	merchantNo := strings.TrimSpace(opts.MerchantNo)
	if merchantNo == "" {
		return nil, fmt.Errorf("merchant_no is required")
	}
	if strings.TrimSpace(opts.MerchantSecret) == "" {
		return nil, fmt.Errorf("merchant_secret is required")
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	copyClient := *hc
	if opts.Timeout > 0 {
		copyClient.Timeout = opts.Timeout
	} else if copyClient.Timeout <= 0 {
		copyClient.Timeout = 10 * time.Second
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	nonceFn := opts.NonceGenerator
	if nonceFn == nil {
		nonceFn = defaultNonce
	}

	c := &Client{
		baseURL:        baseURL,
		merchantNo:     merchantNo,
		merchantSecret: opts.MerchantSecret,
		httpClient:     &copyClient,
		now:            nowFn,
		nonce:          nonceFn,
		userAgent:      strings.TrimSpace(opts.UserAgent),
	}
	c.Merchant = &MerchantAPI{client: c}
	c.Transactions = &TransactionsAPI{client: c}
	return c, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, payload any, out any) error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}
	if strings.TrimSpace(path) == "" || path[0] != '/' {
		return fmt.Errorf("path must start with /")
	}

	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	} else {
		body = []byte{}
	}

	u := *c.baseURL
	u.Path = joinURLPath(c.baseURL.Path, path)
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	timestamp := strconv.FormatInt(c.now().UTC().UnixMilli(), 10)
	nonce := strings.TrimSpace(c.nonce())
	if nonce == "" {
		return fmt.Errorf("nonce generator returned empty value")
	}

	sig := signature(method, path, c.merchantNo, timestamp, nonce, body, c.merchantSecret)
	req.Header.Set("X-Merchant-No", c.merchantNo)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", sig)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(respBody) > maxResponseBodyBytes {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Code:       "RESPONSE_TOO_LARGE",
			Message:    fmt.Sprintf("response body exceeds %d bytes", maxResponseBodyBytes),
			RawBody:    respBody[:maxResponseBodyBytes],
		}
	}

	var env envelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Code:       "INVALID_RESPONSE",
			Message:    string(respBody),
			RawBody:    respBody,
		}
	}

	if env.Code == "" {
		env.Code = "INVALID_RESPONSE"
	}
	if env.Code != successCode || resp.StatusCode >= http.StatusBadRequest {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Code:       env.Code,
			Message:    env.Message,
			RequestID:  env.RequestID,
			RawBody:    respBody,
		}
	}

	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode response data: %w", err)
		}
	}
	return nil
}

func signature(method, path, merchantNo, timestamp, nonce string, body []byte, secret string) string {
	bodyHash := sha256.Sum256(body)
	signing := strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(method)),
		path,
		merchantNo,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signing))
	return hex.EncodeToString(mac.Sum(nil))
}

func defaultNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}

func joinURLPath(basePath, p string) string {
	left := strings.TrimSuffix(strings.TrimSpace(basePath), "/")
	right := strings.TrimPrefix(strings.TrimSpace(p), "/")
	if left == "" {
		return "/" + right
	}
	if !strings.HasPrefix(left, "/") {
		left = "/" + left
	}
	return left + "/" + right
}
