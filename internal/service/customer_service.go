package service

import (
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
