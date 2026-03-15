package db

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dbsqlc "github.com/xmz-ai/coin/internal/db/sqlc"
)

var ErrMerchantNotFound = errors.New("merchant not found")

type SecretCipher interface {
	Encrypt(plain string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type MerchantSecretManager struct {
	pool    *pgxpool.Pool
	cipher  SecretCipher
	queries *dbsqlc.Queries
}

func NewMerchantSecretManager(pool *pgxpool.Pool, cipher SecretCipher) *MerchantSecretManager {
	return &MerchantSecretManager{
		pool:    pool,
		cipher:  cipher,
		queries: dbsqlc.New(pool),
	}
}

func (m *MerchantSecretManager) GetActiveSecret(parent context.Context, merchantNo string) (string, bool, error) {
	ctx, cancel := withParentTimeout(parent)
	defer cancel()

	ciphertext, err := m.queries.GetActiveSecretCiphertext(ctx, merchantNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	secret, err := m.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", false, fmt.Errorf("decrypt merchant secret: %w", err)
	}
	return secret, true, nil
}

func (m *MerchantSecretManager) RotateSecret(parent context.Context, merchantNo string) (string, int, error) {
	ctx, cancel := withParentTimeout(parent)
	defer cancel()

	secret, err := generateMerchantSecret()
	if err != nil {
		return "", 0, err
	}
	ciphertext, err := m.cipher.Encrypt(secret)
	if err != nil {
		return "", 0, fmt.Errorf("encrypt merchant secret: %w", err)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", 0, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := m.queries.WithTx(tx)

	if _, err := qtx.LockMerchantNoForUpdate(ctx, merchantNo); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrMerchantNotFound
		}
		return "", 0, err
	}

	currentVersion, err := qtx.GetMaxSecretVersion(ctx, merchantNo)
	if err != nil {
		return "", 0, err
	}
	nextVersion := currentVersion + 1

	if err := qtx.DeactivateActiveMerchantSecrets(ctx, merchantNo); err != nil {
		return "", 0, err
	}

	if err := qtx.InsertMerchantSecretCredential(ctx, dbsqlc.InsertMerchantSecretCredentialParams{
		MerchantNo:       merchantNo,
		SecretCiphertext: ciphertext,
		SecretVersion:    nextVersion,
	}); err != nil {
		return "", 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return secret, int(nextVersion), nil
}

func (m *MerchantSecretManager) GetSecretVersion(parent context.Context, merchantNo string) (int, error) {
	ctx, cancel := withParentTimeout(parent)
	defer cancel()

	version, err := m.queries.GetMaxSecretVersion(ctx, merchantNo)
	if err != nil {
		return 0, err
	}
	return int(version), nil
}

func generateMerchantSecret() (string, error) {
	raw := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate merchant secret: %w", err)
	}
	return "msk_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func withParentTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 3*time.Second)
}
