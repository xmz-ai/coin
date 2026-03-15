package id

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

const (
	MerchantNoLength = 16
	CustomerNoLength = 16
	AccountNoLength  = 19

	defaultBIN     = "621770"
	defaultRegion  = "000"
	defaultVersion = "1"
	merchantPrefix = "1"
	customerPrefix = "2"
)

var ErrNoMoreFixedCode = errors.New("no more fixed code")

// CodeProvider generates externally visible numeric codes.
type CodeProvider interface {
	NewMerchantNo() (string, error)
	NewCustomerNo() (string, error)
	NewAccountNo(merchantNo, accountType string) (string, error)
}

type runtimeCodeProvider struct {
	mu sync.Mutex

	merchantSeq uint64
	customerSeq uint64
	accountSalt uint32
	accountSeq  map[string]uint32
}

func NewRuntimeCodeProvider() CodeProvider {
	seed := randomUint64()
	return &runtimeCodeProvider{
		merchantSeq: seed % 10_000_000_000,
		customerSeq: (seed / 7) % 10_000_000_000,
		accountSalt: uint32(seed),
		accountSeq:  map[string]uint32{},
	}
}

func (p *runtimeCodeProvider) NewMerchantNo() (string, error) {
	p.mu.Lock()
	p.merchantSeq = (p.merchantSeq + 1) % 10_000_000_000
	seq := p.merchantSeq
	p.mu.Unlock()

	body := fmt.Sprintf("%s%s%s%010d", merchantPrefix, defaultRegion, defaultVersion, seq)
	return appendLuhn(body)
}

func (p *runtimeCodeProvider) NewCustomerNo() (string, error) {
	p.mu.Lock()
	p.customerSeq = (p.customerSeq + 1) % 10_000_000_000
	seq := p.customerSeq
	p.mu.Unlock()

	body := fmt.Sprintf("%s%s%s%010d", customerPrefix, defaultRegion, defaultVersion, seq)
	return appendLuhn(body)
}

func (p *runtimeCodeProvider) NewAccountNo(merchantNo, accountType string) (string, error) {
	mmmm := merchantMapping(merchantNo)
	tt := accountTypeCode(accountType)
	key := mmmm + ":" + tt

	p.mu.Lock()
	seq, ok := p.accountSeq[key]
	if !ok {
		seq = uint32(hashUint64(key+":"+fmt.Sprintf("%d", p.accountSalt)) % 1_000_000)
	}
	seq = (seq + 1) % 1_000_000
	p.accountSeq[key] = seq
	p.mu.Unlock()

	body := fmt.Sprintf("%s%s%s%06d", defaultBIN, mmmm, tt, seq)
	return appendLuhn(body)
}

type fixedCodeProvider struct {
	mu sync.Mutex

	merchantNos []string
	customerNos []string
	accountNos  []string
	mIdx        int
	cIdx        int
	aIdx        int
}

// NewFixedCodeProvider returns deterministic code outputs for tests.
func NewFixedCodeProvider(merchantNos, customerNos, accountNos []string) CodeProvider {
	cpM := append([]string(nil), merchantNos...)
	cpC := append([]string(nil), customerNos...)
	cpA := append([]string(nil), accountNos...)
	return &fixedCodeProvider{
		merchantNos: cpM,
		customerNos: cpC,
		accountNos:  cpA,
	}
}

func (p *fixedCodeProvider) NewMerchantNo() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mIdx >= len(p.merchantNos) {
		return "", ErrNoMoreFixedCode
	}
	v := p.merchantNos[p.mIdx]
	p.mIdx++
	return v, nil
}

func (p *fixedCodeProvider) NewCustomerNo() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cIdx >= len(p.customerNos) {
		return "", ErrNoMoreFixedCode
	}
	v := p.customerNos[p.cIdx]
	p.cIdx++
	return v, nil
}

func (p *fixedCodeProvider) NewAccountNo(_, _ string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.aIdx >= len(p.accountNos) {
		return "", ErrNoMoreFixedCode
	}
	v := p.accountNos[p.aIdx]
	p.aIdx++
	return v, nil
}

func IsValidMerchantNo(v string) bool {
	return hasExactDigits(v, MerchantNoLength) && strings.HasPrefix(v, merchantPrefix) && isValidLuhn(v)
}

func IsValidCustomerNo(v string) bool {
	return hasExactDigits(v, CustomerNoLength) && strings.HasPrefix(v, customerPrefix) && isValidLuhn(v)
}

func IsValidAccountNo(v string) bool {
	return hasExactDigits(v, AccountNoLength) && isValidLuhn(v)
}

func appendLuhn(body string) (string, error) {
	if !onlyDigits(body) {
		return "", errors.New("luhn body contains non-digit")
	}
	check := computeLuhnCheckDigit(body)
	return body + string(check), nil
}

func computeLuhnCheckDigit(body string) byte {
	sum := 0
	double := true
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	check := (10 - (sum % 10)) % 10
	return byte('0' + check)
}

func isValidLuhn(v string) bool {
	if !onlyDigits(v) || v == "" {
		return false
	}
	sum := 0
	double := false
	for i := len(v) - 1; i >= 0; i-- {
		d := int(v[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

func hasExactDigits(v string, n int) bool {
	return len(v) == n && onlyDigits(v)
}

func onlyDigits(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

func merchantMapping(merchantNo string) string {
	h := hashUint64(strings.TrimSpace(merchantNo))
	return fmt.Sprintf("%04d", h%10000)
}

func accountTypeCode(accountType string) string {
	switch strings.ToUpper(strings.TrimSpace(accountType)) {
	case "BUDGET":
		return "01"
	case "RECEIVABLE":
		return "02"
	case "CUSTOMER", "CUSTOM":
		return "10"
	default:
		return "11"
	}
}

func hashUint64(v string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(v))
	return hasher.Sum64()
}

func randomUint64() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint64(b[:])
}
