package id

import (
	"errors"
	"fmt"
	"sync"
)

const (
	defaultLeaseBatchSize = int64(100)
	defaultLowWatermark   = int64(20)

	globalScopeKey = "GLOBAL"

	codeTypeMerchantNo = "MERCHANT_NO"
	codeTypeCustomerNo = "CUSTOMER_NO"
	codeTypeAccountNo  = "ACCOUNT_NO"

	merchantSeqDigits = 10
	customerSeqDigits = 10
	accountSeqDigits  = 6
)

var (
	ErrCodeAllocatorUnavailable = errors.New("code allocator unavailable")
	ErrCodeSequenceExhausted    = errors.New("code sequence exhausted")
)

// SequenceRangeStore leases non-overlapping numeric ranges by code type and scope.
type SequenceRangeStore interface {
	LeaseRange(codeType, scopeKey string, batchSize int64) (startValue, endValue int64, err error)
}

type HiLoCodeProviderOptions struct {
	BatchSize    int64
	LowWatermark int64
}

type dbCodeProvider struct {
	store        SequenceRangeStore
	batchSize    int64
	lowWatermark int64
	buckets      sync.Map
}

type codeSegment struct {
	next  int64
	end   int64
	valid bool
}

func (s *codeSegment) hasNext() bool {
	return s.valid && s.next <= s.end
}

func (s *codeSegment) take() int64 {
	v := s.next
	s.next++
	if s.next > s.end {
		s.valid = false
	}
	return v
}

func (s *codeSegment) remaining() int64 {
	if !s.hasNext() {
		return 0
	}
	return s.end - s.next + 1
}

type codeBucket struct {
	mu       sync.Mutex
	cond     *sync.Cond
	current  codeSegment
	prefetch *codeSegment
	renewing bool
}

func newCodeBucket() *codeBucket {
	b := &codeBucket{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func NewDBCodeProvider(store SequenceRangeStore, options ...HiLoCodeProviderOptions) CodeProvider {
	batchSize := defaultLeaseBatchSize
	lowWatermark := defaultLowWatermark
	if len(options) > 0 {
		if options[0].BatchSize > 0 {
			batchSize = options[0].BatchSize
		}
		if options[0].LowWatermark >= 0 {
			lowWatermark = options[0].LowWatermark
		}
	}
	if lowWatermark >= batchSize {
		lowWatermark = batchSize / 2
	}
	if lowWatermark < 0 {
		lowWatermark = 0
	}

	return &dbCodeProvider{
		store:        store,
		batchSize:    batchSize,
		lowWatermark: lowWatermark,
	}
}

func (p *dbCodeProvider) NewMerchantNo() (string, error) {
	seq, err := p.nextSeq(codeTypeMerchantNo, globalScopeKey)
	if err != nil {
		return "", err
	}
	if seq >= pow10(merchantSeqDigits) {
		return "", ErrCodeSequenceExhausted
	}
	body := fmt.Sprintf("%s%s%s%010d", merchantPrefix, defaultRegion, defaultVersion, seq)
	return appendLuhn(body)
}

func (p *dbCodeProvider) NewCustomerNo() (string, error) {
	seq, err := p.nextSeq(codeTypeCustomerNo, globalScopeKey)
	if err != nil {
		return "", err
	}
	if seq >= pow10(customerSeqDigits) {
		return "", ErrCodeSequenceExhausted
	}
	body := fmt.Sprintf("%s%s%s%010d", customerPrefix, defaultRegion, defaultVersion, seq)
	return appendLuhn(body)
}

func (p *dbCodeProvider) NewAccountNo(merchantNo, accountType string) (string, error) {
	mmmm := merchantMapping(merchantNo)
	tt := accountTypeCode(accountType)
	scopeKey := defaultBIN + mmmm + tt

	seq, err := p.nextSeq(codeTypeAccountNo, scopeKey)
	if err != nil {
		return "", err
	}
	if seq >= pow10(accountSeqDigits) {
		return "", ErrCodeSequenceExhausted
	}

	body := fmt.Sprintf("%s%s%s%06d", defaultBIN, mmmm, tt, seq)
	return appendLuhn(body)
}

func (p *dbCodeProvider) nextSeq(codeType, scopeKey string) (int64, error) {
	if p == nil || p.store == nil {
		return 0, ErrCodeAllocatorUnavailable
	}
	bucket := p.bucketOf(codeType + ":" + scopeKey)

	for {
		bucket.mu.Lock()
		if bucket.current.hasNext() {
			seq := bucket.current.take()
			if bucket.current.remaining() <= p.lowWatermark && bucket.prefetch == nil && !bucket.renewing {
				bucket.renewing = true
				go p.renewAsync(bucket, codeType, scopeKey)
			}
			bucket.mu.Unlock()
			return seq, nil
		}
		if bucket.prefetch != nil && bucket.prefetch.hasNext() {
			bucket.current = *bucket.prefetch
			bucket.prefetch = nil
			bucket.mu.Unlock()
			continue
		}
		if bucket.renewing {
			for !bucket.current.hasNext() && (bucket.prefetch == nil || !bucket.prefetch.hasNext()) && bucket.renewing {
				bucket.cond.Wait()
			}
			bucket.mu.Unlock()
			continue
		}

		bucket.renewing = true
		bucket.mu.Unlock()

		start, end, err := p.store.LeaseRange(codeType, scopeKey, p.batchSize)

		bucket.mu.Lock()
		if err == nil {
			bucket.current = codeSegment{next: start, end: end, valid: true}
		}
		bucket.renewing = false
		bucket.cond.Broadcast()
		bucket.mu.Unlock()

		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrCodeAllocatorUnavailable, err)
		}
	}
}

func (p *dbCodeProvider) renewAsync(bucket *codeBucket, codeType, scopeKey string) {
	start, end, err := p.store.LeaseRange(codeType, scopeKey, p.batchSize)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	if err == nil {
		bucket.prefetch = &codeSegment{next: start, end: end, valid: true}
	}
	bucket.renewing = false
	bucket.cond.Broadcast()
}

func (p *dbCodeProvider) bucketOf(key string) *codeBucket {
	loaded, ok := p.buckets.Load(key)
	if ok {
		return loaded.(*codeBucket)
	}
	b := newCodeBucket()
	actual, _ := p.buckets.LoadOrStore(key, b)
	return actual.(*codeBucket)
}

func pow10(n int) int64 {
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}
