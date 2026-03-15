package service

import (
	"sort"
	"time"
)

type accountBook struct {
	ExpireAt time.Time
	Balance  int64
}

type ExpiryBookService struct {
	repo  Repository
	nowFn func() time.Time
	books map[string][]accountBook
}

func NewExpiryBookService(repo Repository) *ExpiryBookService {
	return &ExpiryBookService{
		repo:  repo,
		nowFn: func() time.Time { return time.Now().UTC() },
		books: map[string][]accountBook{},
	}
}

func (s *ExpiryBookService) SetNow(now time.Time) {
	s.nowFn = func() time.Time { return now.UTC() }
}

func (s *ExpiryBookService) Credit(accountNo string, amount int64, expireAt time.Time) error {
	a, ok := s.repo.GetAccount(accountNo)
	if !ok {
		return ErrAccountResolveFailed
	}
	if !a.BookEnabled {
		return ErrBookDisabled
	}
	if expireAt.IsZero() {
		return ErrExpireAtRequired
	}

	books := s.books[accountNo]
	found := false
	for i := range books {
		if books[i].ExpireAt.Equal(expireAt.UTC()) {
			books[i].Balance += amount
			found = true
			break
		}
	}
	if !found {
		books = append(books, accountBook{ExpireAt: expireAt.UTC(), Balance: amount})
	}
	s.books[accountNo] = books
	a.Balance += amount
	_ = s.repo.CreateAccount(a)
	return nil
}

func (s *ExpiryBookService) Debit(accountNo string, amount int64) ([]BookPart, error) {
	a, ok := s.repo.GetAccount(accountNo)
	if !ok {
		return nil, ErrAccountResolveFailed
	}
	if !a.BookEnabled {
		return nil, ErrBookDisabled
	}
	now := s.nowFn()

	books := s.books[accountNo]
	sort.Slice(books, func(i, j int) bool {
		return books[i].ExpireAt.Before(books[j].ExpireAt)
	})

	left := amount
	parts := make([]BookPart, 0)
	for i := range books {
		if left == 0 {
			break
		}
		if !books[i].ExpireAt.After(now) {
			continue
		}
		if books[i].Balance <= 0 {
			continue
		}
		use := books[i].Balance
		if use > left {
			use = left
		}
		books[i].Balance -= use
		left -= use
		parts = append(parts, BookPart{ExpireAt: books[i].ExpireAt, Amount: use})
	}
	s.books[accountNo] = books
	applied := amount - left
	a.Balance -= applied
	_ = s.repo.CreateAccount(a)
	return parts, nil
}

func (s *ExpiryBookService) VerifyAccountBookBalance(accountNo string) bool {
	a, ok := s.repo.GetAccount(accountNo)
	if !ok {
		return false
	}
	sum := int64(0)
	for _, b := range s.books[accountNo] {
		sum += b.Balance
	}
	return a.Balance == sum
}
