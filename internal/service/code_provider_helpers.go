package service

import (
	"errors"
	"fmt"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
)

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
