package coin

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// CustomersAPI contains customer-related calls for merchant apps.
type CustomersAPI struct {
	client *Client
}

// CustomerBalance represents the balance response for a customer.
type CustomerBalance struct {
	OutUserID         string `json:"out_user_id"`
	AccountNo         string `json:"account_no"`
	Balance           int64  `json:"balance"`
	AvailableBalance  int64  `json:"available_balance"`
	BookEnabled       bool   `json:"book_enabled"`
}

// GetBalance retrieves the balance for a customer identified by out_user_id.
func (c *CustomersAPI) GetBalance(ctx context.Context, outUserID string) (CustomerBalance, error) {
	outUserID = strings.TrimSpace(outUserID)
	if outUserID == "" {
		return CustomerBalance{}, fmt.Errorf("out_user_id is required")
	}
	q := url.Values{}
	q.Set("out_user_id", outUserID)
	var out CustomerBalance
	if err := c.client.do(ctx, "GET", "/api/v1/customers/balance", q, nil, &out); err != nil {
		return CustomerBalance{}, err
	}
	return out, nil
}
