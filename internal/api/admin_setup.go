package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/db"
	"github.com/xmz-ai/coin/internal/service"
	"golang.org/x/crypto/bcrypt"
)

var adminSetupUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)

var (
	errAdminSetupAlreadyInitialized = errors.New("admin setup already initialized")
	errAdminSetupAdminUserExists    = errors.New("admin user already exists")
)

type adminSetupController struct {
	repo            *db.Repository
	merchantService *service.MerchantService
	secretRotator   MerchantSecretRotator
}

type adminSetupInitializeRequest struct {
	AdminUsername      string `json:"admin_username"`
	AdminPassword      string `json:"admin_password"`
	MerchantName       string `json:"merchant_name"`
	MerchantWebhookURL string `json:"merchant_webhook_url"`
}

func newAdminSetupController(repo *db.Repository, merchantService *service.MerchantService, secretRotator MerchantSecretRotator) (*adminSetupController, error) {
	if repo == nil {
		return nil, errors.New("admin setup repo is required")
	}
	if merchantService == nil {
		return nil, errors.New("admin setup merchant service is required")
	}
	if secretRotator == nil {
		return nil, errors.New("admin setup secret rotator is required")
	}
	return &adminSetupController{
		repo:            repo,
		merchantService: merchantService,
		secretRotator:   secretRotator,
	}, nil
}

func (s *adminSetupController) handleStatus(c *gin.Context) {
	if s == nil || s.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin setup not configured")
		return
	}

	state, err := s.repo.GetAdminSetupState()
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load admin setup state failed")
		return
	}

	writeSuccess(c, gin.H{
		"initialized": state.Initialized,
	})
}

func (s *adminSetupController) handleInitialize(c *gin.Context) {
	if s == nil || s.repo == nil || s.merchantService == nil || s.secretRotator == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin setup not configured")
		return
	}

	var req adminSetupInitializeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.AdminUsername = strings.TrimSpace(req.AdminUsername)
	req.AdminPassword = strings.TrimSpace(req.AdminPassword)
	req.MerchantName = strings.TrimSpace(req.MerchantName)
	req.MerchantWebhookURL = strings.TrimSpace(req.MerchantWebhookURL)
	if !adminSetupUsernamePattern.MatchString(req.AdminUsername) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid admin_username")
		return
	}
	if len(req.AdminPassword) < 8 || len(req.AdminPassword) > 72 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "admin_password length must be 8~72")
		return
	}
	if req.MerchantName == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_name is required")
		return
	}
	if req.MerchantWebhookURL != "" && !strings.HasPrefix(strings.ToLower(req.MerchantWebhookURL), "https://") {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_webhook_url must be https")
		return
	}

	var result gin.H
	err := s.repo.WithAdminSetupLock(func() error {
		state, err := s.repo.GetAdminSetupState()
		if err != nil {
			return err
		}
		if state.Initialized {
			return errAdminSetupAlreadyInitialized
		}

		existing, foundByUsername, err := s.repo.GetAdminUserByUsername(req.AdminUsername)
		if err != nil {
			return err
		}
		passwordHashRaw, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		passwordHash := string(passwordHashRaw)
		if foundByUsername {
			if err := s.repo.EnsureAdminUser(existing.Username, passwordHash); err != nil {
				return err
			}
		} else {
			if err := s.repo.CreateAdminUser(req.AdminUsername, passwordHash); err != nil {
				if errors.Is(err, db.ErrAdminUserExists) {
					return errAdminSetupAdminUserExists
				}
				return err
			}
		}
		if err := s.repo.SaveAdminSetupProgress(req.AdminUsername, state.DefaultMerchantNo); err != nil {
			return err
		}

		merchantNo := strings.TrimSpace(state.DefaultMerchantNo)
		merchant, ok := s.merchantService.GetMerchantConfigByNo(merchantNo)
		if merchantNo == "" || !ok {
			created, err := s.merchantService.CreateMerchant("", req.MerchantName)
			if err != nil {
				return err
			}
			merchant = created
			merchantNo = created.MerchantNo
			if err := s.repo.SaveAdminSetupProgress(req.AdminUsername, merchantNo); err != nil {
				return err
			}
		}
		if err := s.merchantService.UpsertMerchantFeatureConfig(merchantNo, true, true); err != nil {
			return err
		}
		if req.MerchantWebhookURL == "" {
			if err := s.repo.UpsertWebhookConfig(merchantNo, "", false); err != nil {
				return err
			}
		} else {
			if err := s.repo.UpsertWebhookConfig(merchantNo, req.MerchantWebhookURL, true); err != nil {
				return err
			}
		}

		secret, version, err := s.secretRotator.RotateSecret(c.Request.Context(), merchantNo)
		if err != nil {
			return err
		}
		if err := s.repo.MarkAdminSetupInitialized(req.AdminUsername, merchantNo); err != nil {
			return err
		}

		result = gin.H{
			"admin_username":        req.AdminUsername,
			"merchant_no":           merchantNo,
			"merchant_secret":       secret,
			"secret_version":        version,
			"budget_account_no":     merchant.BudgetAccountNo,
			"receivable_account_no": merchant.ReceivableAccountNo,
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errAdminSetupAlreadyInitialized):
			writeError(c, http.StatusConflict, "ADMIN_SETUP_ALREADY_INITIALIZED", "admin setup already initialized")
		case errors.Is(err, errAdminSetupAdminUserExists):
			writeError(c, http.StatusConflict, "ADMIN_SETUP_ADMIN_USER_EXISTS", "admin user already exists")
		default:
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin setup initialize failed")
		}
		return
	}

	writeCreated(c, result)
}
