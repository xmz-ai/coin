package service

import (
	"errors"
	"fmt"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
)

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
	c, err := s.createCustomerOnly(merchantNo, outUserID)
	if err != nil {
		return Customer{}, err
	}

	cfg, err := s.loadMerchantFeatureConfig(merchantNo)
	if err != nil {
		return Customer{}, err
	}
	if !cfg.AutoCreateAccountOnCustomerCreate {
		return c, nil
	}
	if _, err := s.ensureDefaultCustomerAccount(merchantNo, c.CustomerNo); err != nil {
		return Customer{}, err
	}
	return c, nil
}

func (s *CustomerService) EnsureCustomerAccountForCredit(merchantNo, outUserID string) (string, error) {
	merchant, ok := s.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return "", ErrInvalidMerchantNo
	}

	cfg, err := s.loadMerchantFeatureConfig(merchant.MerchantNo)
	if err != nil {
		return "", err
	}
	if !cfg.AutoCreateCustomerOnCredit {
		return "", nil
	}

	customer, ok := s.repo.GetCustomerByOutUserID(merchant.MerchantNo, outUserID)
	if !ok {
		customer, err = s.createCustomerOnly(merchant.MerchantNo, outUserID)
		if err != nil {
			if !errors.Is(err, ErrCustomerExists) {
				return "", err
			}
			loaded, exists := s.repo.GetCustomerByOutUserID(merchant.MerchantNo, outUserID)
			if !exists {
				return "", err
			}
			customer = loaded
		}
	}

	return s.ensureDefaultCustomerAccount(merchant.MerchantNo, customer.CustomerNo)
}

func (s *CustomerService) createCustomerOnly(merchantNo, outUserID string) (Customer, error) {
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

func (s *CustomerService) ensureDefaultCustomerAccount(merchantNo, customerNo string) (string, error) {
	if existing, ok := s.repo.GetAccountByCustomerNo(merchantNo, customerNo); ok && existing.AccountNo != "" {
		return existing.AccountNo, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		accountNo, err := s.codes.NewAccountNo(merchantNo, "CUSTOMER")
		if err != nil {
			return "", mapCodeError("new customer account no", err)
		}
		if !idpkg.IsValidAccountNo(accountNo) {
			return "", ErrInvalidAccountNo
		}
		err = s.repo.CreateAccount(Account{
			AccountNo:         accountNo,
			MerchantNo:        merchantNo,
			CustomerNo:        customerNo,
			AccountType:       "CUSTOMER",
			AllowOverdraft:    false,
			MaxOverdraftLimit: 0,
			AllowDebitOut:     true,
			AllowCreditIn:     true,
			AllowTransfer:     true,
			BookEnabled:       true,
			Balance:           0,
		})
		if err == nil {
			return accountNo, nil
		}
		if !errors.Is(err, ErrAccountNoExists) {
			return "", err
		}
	}
	return "", ErrAccountNoExists
}

func (s *CustomerService) loadMerchantFeatureConfig(merchantNo string) (MerchantFeatureConfig, error) {
	cfg, found, err := s.repo.GetMerchantFeatureConfig(merchantNo)
	if err != nil {
		return MerchantFeatureConfig{}, err
	}
	if !found {
		return MerchantFeatureConfig{
			AutoCreateAccountOnCustomerCreate: true,
			AutoCreateCustomerOnCredit:        true,
		}, nil
	}
	return cfg, nil
}

func (s *CustomerService) GetCustomerByOutUserID(merchantNo, outUserID string) (Customer, bool) {
	return s.repo.GetCustomerByOutUserID(merchantNo, outUserID)
}
