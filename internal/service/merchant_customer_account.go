package service

import (
	"errors"
	"fmt"
	"strings"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
)

type MerchantService struct {
	repo  Repository
	ids   idpkg.UUIDProvider
	codes idpkg.CodeProvider
}

func NewMerchantService(repo Repository, ids idpkg.UUIDProvider, codeProviders ...idpkg.CodeProvider) *MerchantService {
	return &MerchantService{
		repo:  repo,
		ids:   ids,
		codes: pickCodeProvider(repo, codeProviders),
	}
}

func (s *MerchantService) CreateMerchant(merchantNo, name string) (Merchant, error) {
	merchantID, err := s.ids.NewUUIDv7()
	if err != nil {
		return Merchant{}, fmt.Errorf("new merchant id: %w", err)
	}

	fixedMerchantNo := strings.TrimSpace(merchantNo)
	if fixedMerchantNo != "" && !idpkg.IsValidMerchantNo(fixedMerchantNo) {
		return Merchant{}, ErrInvalidMerchantNo
	}

	resolvedMerchantNo := fixedMerchantNo
	if resolvedMerchantNo == "" {
		resolvedMerchantNo, err = s.codes.NewMerchantNo()
		if err != nil {
			return Merchant{}, mapCodeError("new merchant no", err)
		}
	}

	budget, err := s.codes.NewAccountNo(resolvedMerchantNo, AccountTypeBudget)
	if err != nil {
		return Merchant{}, mapCodeError("new budget account no", err)
	}
	recv, err := s.codes.NewAccountNo(resolvedMerchantNo, AccountTypeReceivable)
	if err != nil {
		return Merchant{}, mapCodeError("new receivable account no", err)
	}
	if budget == recv {
		return Merchant{}, ErrInvalidAccountNo
	}
	if !idpkg.IsValidAccountNo(budget) || !idpkg.IsValidAccountNo(recv) {
		return Merchant{}, ErrInvalidAccountNo
	}

	m := Merchant{
		MerchantID:          merchantID,
		MerchantNo:          resolvedMerchantNo,
		Name:                name,
		BudgetAccountNo:     budget,
		ReceivableAccountNo: recv,
	}
	if err := s.repo.CreateMerchant(m); err != nil {
		return Merchant{}, err
	}

	if err := s.repo.CreateAccount(Account{
		AccountNo:         budget,
		MerchantNo:        resolvedMerchantNo,
		AccountType:       AccountTypeBudget,
		AllowOverdraft:    true,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           0,
	}); err != nil {
		return Merchant{}, err
	}
	if err := s.repo.CreateAccount(Account{
		AccountNo:     recv,
		MerchantNo:    resolvedMerchantNo,
		AccountType:   AccountTypeReceivable,
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		return Merchant{}, err
	}
	return m, nil
}

func (s *MerchantService) GetMerchantConfigByNo(merchantNo string) (Merchant, bool) {
	return s.repo.GetMerchantByNo(merchantNo)
}

func (s *MerchantService) GetAccountByNo(accountNo string) (Account, bool) {
	return s.repo.GetAccount(accountNo)
}

type CustomerService struct {
	repo  Repository
	ids   idpkg.UUIDProvider
	codes idpkg.CodeProvider
}

func NewCustomerService(repo Repository, ids idpkg.UUIDProvider, codeProviders ...idpkg.CodeProvider) *CustomerService {
	return &CustomerService{
		repo:  repo,
		ids:   ids,
		codes: pickCodeProvider(repo, codeProviders),
	}
}

func (s *CustomerService) CreateCustomer(merchantNo, outUserID string) (Customer, error) {
	merchant, ok := s.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return Customer{}, ErrInvalidMerchantNo
	}

	if existing, ok := s.repo.GetCustomerByOutUserID(merchantNo, outUserID); ok && existing.CustomerID != "" {
		return Customer{}, ErrCustomerExists
	}

	customerID, err := s.ids.NewUUIDv7()
	if err != nil {
		return Customer{}, fmt.Errorf("new customer id: %w", err)
	}

	customerNo, err := s.codes.NewCustomerNo()
	if err != nil {
		return Customer{}, mapCodeError("new customer no", err)
	}
	if !idpkg.IsValidCustomerNo(customerNo) {
		return Customer{}, ErrInvalidCustomerNo
	}

	c := Customer{
		CustomerID: customerID,
		CustomerNo: customerNo,
		MerchantNo: merchant.MerchantNo,
		OutUserID:  outUserID,
	}
	if err := s.repo.CreateCustomer(c); err != nil {
		return Customer{}, err
	}
	return c, nil
}

func (s *CustomerService) GetCustomerByOutUserID(merchantNo, outUserID string) (Customer, bool) {
	return s.repo.GetCustomerByOutUserID(merchantNo, outUserID)
}

func pickCodeProvider(repo Repository, codeProviders []idpkg.CodeProvider) idpkg.CodeProvider {
	if len(codeProviders) > 0 && codeProviders[0] != nil {
		return codeProviders[0]
	}
	if repoCodeProvider, ok := repo.(idpkg.CodeProvider); ok {
		return repoCodeProvider
	}
	return idpkg.NewRuntimeCodeProvider()
}

func mapCodeError(op string, err error) error {
	if errors.Is(err, idpkg.ErrCodeAllocatorUnavailable) {
		return ErrCodeAllocatorUnavailable
	}
	return fmt.Errorf("%s: %w", op, err)
}

func rightDigits(v string, n int) string {
	digits := make([]rune, 0, len(v))
	for _, ch := range v {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, ch)
		}
	}
	s := string(digits)
	if len(s) >= n {
		return s[len(s)-n:]
	}
	return strings.Repeat("0", n-len(s)) + s
}
