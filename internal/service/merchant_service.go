package service

import (
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
