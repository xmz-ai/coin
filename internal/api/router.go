package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var ErrMerchantNoMismatch = errors.New("merchant_no mismatch")

type MerchantSecretRotator interface {
	RotateSecret(ctx context.Context, merchantNo string) (secret string, version int, err error)
}

type MerchantSecretVersionReader interface {
	GetSecretVersion(ctx context.Context, merchantNo string) (version int, err error)
}

type ProtectedRoutesOptions struct {
	AuthMiddleware gin.HandlerFunc
	SecretRotator  MerchantSecretRotator
	Business       *BusinessHandler
}

func NewRouter() *gin.Engine {
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "ok", "request_id": getRequestID(c)})
	})

	return r
}

func RegisterProtectedRoutes(r *gin.Engine, opts ProtectedRoutesOptions) {
	if r == nil || opts.AuthMiddleware == nil {
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
