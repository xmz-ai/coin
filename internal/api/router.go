package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/service"
)

var ErrMerchantNoMismatch = errors.New("merchant_no mismatch")

type MerchantSecretRotator interface {
	RotateSecret(ctx context.Context, merchantNo string) (secret string, version int, err error)
}

type MerchantSecretVersionReader interface {
	GetSecretVersion(ctx context.Context, merchantNo string) (version int, err error)
}

type ProtectedRoutesOptions struct {
	AuthMiddleware  gin.HandlerFunc
	SecretRotator   MerchantSecretRotator
	Business        *BusinessHandler
	MerchantCreator MerchantCreator
}

type MerchantCreator interface {
	CreateMerchant(merchantNo, name string) (service.Merchant, error)
	UpsertMerchantFeatureConfig(merchantNo string, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit bool) error
	GetMerchantFeatureConfig(merchantNo string) (service.MerchantFeatureConfig, bool, error)
}

type RouterOptions struct {
	EnablePprof bool
}

func NewRouter(options ...RouterOptions) *gin.Engine {
	var opts RouterOptions
	if len(options) > 0 {
		opts = options[0]
	}

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "ok", "request_id": getRequestID(c)})
	})
	if opts.EnablePprof {
		registerPprofRoutes(r)
	}

	return r
}

func registerPprofRoutes(r *gin.Engine) {
	if r == nil {
		return
	}
	group := r.Group("/debug/pprof")
	group.GET("/", gin.WrapF(pprof.Index))
	group.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	group.GET("/profile", gin.WrapF(pprof.Profile))
	group.GET("/symbol", gin.WrapF(pprof.Symbol))
	group.POST("/symbol", gin.WrapF(pprof.Symbol))
	group.GET("/trace", gin.WrapF(pprof.Trace))
	group.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
	group.GET("/block", gin.WrapH(pprof.Handler("block")))
	group.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
	group.GET("/heap", gin.WrapH(pprof.Handler("heap")))
	group.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
	group.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
}

func RegisterProtectedRoutes(r *gin.Engine, opts ProtectedRoutesOptions) {
	if r == nil {
		return
	}

	v1Public := r.Group("/api/v1")
	if opts.MerchantCreator != nil && opts.SecretRotator != nil {
		v1Public.POST("/merchants", func(c *gin.Context) {
			var req struct {
				Name                              string `json:"name"`
				AutoCreateAccountOnCustomerCreate *bool  `json:"auto_create_account_on_customer_create"`
				AutoCreateCustomerOnCredit        *bool  `json:"auto_create_customer_on_credit"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
				return
			}
			req.Name = strings.TrimSpace(req.Name)
			if req.Name == "" {
				writeError(c, http.StatusBadRequest, "INVALID_PARAM", "name is required")
				return
			}

			autoCreateAccountOnCustomerCreate := true
			if req.AutoCreateAccountOnCustomerCreate != nil {
				autoCreateAccountOnCustomerCreate = *req.AutoCreateAccountOnCustomerCreate
			}
			autoCreateCustomerOnCredit := true
			if req.AutoCreateCustomerOnCredit != nil {
				autoCreateCustomerOnCredit = *req.AutoCreateCustomerOnCredit
			}

			merchant, err := opts.MerchantCreator.CreateMerchant("", req.Name)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "create merchant failed")
				return
			}
			if err := opts.MerchantCreator.UpsertMerchantFeatureConfig(merchant.MerchantNo, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit); err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "save merchant feature config failed")
				return
			}
			secret, version, err := opts.SecretRotator.RotateSecret(c.Request.Context(), merchant.MerchantNo)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "rotate merchant secret failed")
				return
			}

			writeCreated(c, gin.H{
				"merchant_no":                            merchant.MerchantNo,
				"merchant_secret":                        secret,
				"budget_account_no":                      merchant.BudgetAccountNo,
				"receivable_account_no":                  merchant.ReceivableAccountNo,
				"writeoff_account_no":                    merchant.WriteoffAccountNo,
				"secret_version":                         version,
				"auto_create_account_on_customer_create": autoCreateAccountOnCustomerCreate,
				"auto_create_customer_on_credit":         autoCreateCustomerOnCredit,
			})
		})
	}

	if opts.AuthMiddleware == nil {
		return
	}

	v1 := r.Group("/api/v1")
	v1.Use(opts.AuthMiddleware)

	v1.GET("/merchants/me", func(c *gin.Context) {
		merchantNo, ok := MerchantNoFromContext(c)
		if !ok {
			writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
			return
		}

		data := gin.H{
			"merchant_no":    merchantNo,
			"status":         "ACTIVE",
			"secret_version": 0,
		}
		if versionReader, ok := opts.SecretRotator.(MerchantSecretVersionReader); ok {
			version, err := versionReader.GetSecretVersion(c.Request.Context(), merchantNo)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant secret version failed")
				return
			}
			data["secret_version"] = version
		}
		if opts.Business != nil && opts.Business.merchants != nil {
			merchant, found := opts.Business.merchants.GetMerchantByNo(merchantNo)
			if !found {
				writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
				return
			}
			data["name"] = merchant.Name
			data["budget_account_no"] = merchant.BudgetAccountNo
			data["receivable_account_no"] = merchant.ReceivableAccountNo
			data["writeoff_account_no"] = merchant.WriteoffAccountNo
		}
		if opts.MerchantCreator != nil {
			featureCfg, found, err := opts.MerchantCreator.GetMerchantFeatureConfig(merchantNo)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant feature config failed")
				return
			}
			if found {
				data["auto_create_account_on_customer_create"] = featureCfg.AutoCreateAccountOnCustomerCreate
				data["auto_create_customer_on_credit"] = featureCfg.AutoCreateCustomerOnCredit
			} else {
				data["auto_create_account_on_customer_create"] = true
				data["auto_create_customer_on_credit"] = true
			}
		}

		writeSuccess(c, data)
	})

	if opts.SecretRotator != nil {
		v1.POST("/merchants/:merchant_no/secret:rotate", func(c *gin.Context) {
			authMerchantNo, ok := MerchantNoFromContext(c)
			if !ok {
				writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
				return
			}

			pathMerchantNo := strings.TrimSpace(c.Param("merchant_no"))
			if pathMerchantNo == "" || pathMerchantNo != authMerchantNo {
				writeError(c, http.StatusForbidden, "INVALID_PARAM", ErrMerchantNoMismatch.Error())
				return
			}

			secret, version, err := opts.SecretRotator.RotateSecret(c.Request.Context(), authMerchantNo)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "rotate merchant secret failed")
				return
			}

			writeSuccess(c, gin.H{
				"merchant_no":     authMerchantNo,
				"merchant_secret": secret,
				"secret_version":  version,
			})
		})
	}

	if opts.Business != nil {
		opts.Business.Register(v1)
	}
}
