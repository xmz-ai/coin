package coin

import "context"

// MerchantAPI contains read-only merchant profile calls for merchant apps.
type MerchantAPI struct {
	client *Client
}

// Me reads current authenticated merchant profile.
func (m *MerchantAPI) Me(ctx context.Context) (MerchantProfile, error) {
	var out MerchantProfile
	if err := m.client.do(ctx, "GET", "/api/v1/merchants/me", nil, nil, &out); err != nil {
		return MerchantProfile{}, err
	}
	return out, nil
}
