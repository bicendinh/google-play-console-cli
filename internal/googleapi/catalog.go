package googleapi

import (
	"context"
	pub "google.golang.org/api/androidpublisher/v3"
	"playops/internal/fault"
)

func pages[T any](fetch func(string) ([]T, string, error)) ([]T, error) {
	out := []T{}
	seen := map[string]bool{}
	token := ""
	for {
		rows, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if next == "" {
			return out, nil
		}
		if seen[next] {
			return nil, fault.New("PAGINATION_ERROR", "Google returned a repeated page token", 5)
		}
		seen[next] = true
		token = next
	}
}
func (c *Client) Subscriptions(ctx context.Context, app, id string, offers bool) (map[string]any, error) {
	rows := []*pub.Subscription{}
	if id != "" {
		r, err := c.Publisher.Monetization.Subscriptions.Get(app, id).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	} else {
		var err error
		rows, err = pages(func(token string) ([]*pub.Subscription, string, error) {
			r, e := c.Publisher.Monetization.Subscriptions.List(app).PageToken(token).PageSize(1000).Context(ctx).Do()
			if e != nil {
				return nil, "", e
			}
			return r.Subscriptions, r.NextPageToken, nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := map[string]any{"subscriptions": rows}
	if offers {
		product := id
		if product == "" {
			product = "-"
		}
		rows, err := pages(func(token string) ([]*pub.SubscriptionOffer, string, error) {
			r, e := c.Publisher.Monetization.Subscriptions.BasePlans.Offers.List(app, product, "-").PageToken(token).PageSize(1000).Context(ctx).Do()
			if e != nil {
				return nil, "", e
			}
			return r.SubscriptionOffers, r.NextPageToken, nil
		})
		if err != nil {
			return nil, err
		}
		result["offers"] = rows
	}
	return result, nil
}
func (c *Client) Products(ctx context.Context, app, id string, offers, legacy bool) (map[string]any, error) {
	if legacy {
		if id != "" {
			r, err := c.Publisher.Inappproducts.Get(app, id).Context(ctx).Do()
			return map[string]any{"products": []*pub.InAppProduct{r}}, err
		}
		rows, err := pages(func(token string) ([]*pub.InAppProduct, string, error) {
			r, e := c.Publisher.Inappproducts.List(app).Token(token).Context(ctx).Do()
			if e != nil {
				return nil, "", e
			}
			next := ""
			if r.TokenPagination != nil {
				next = r.TokenPagination.NextPageToken
			}
			return r.Inappproduct, next, nil
		})
		return map[string]any{"products": rows}, err
	}
	rows := []*pub.OneTimeProduct{}
	if id != "" {
		r, err := c.Publisher.Monetization.Onetimeproducts.Get(app, id).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	} else {
		var err error
		rows, err = pages(func(token string) ([]*pub.OneTimeProduct, string, error) {
			r, e := c.Publisher.Monetization.Onetimeproducts.List(app).PageToken(token).PageSize(1000).Context(ctx).Do()
			if e != nil {
				return nil, "", e
			}
			return r.OneTimeProducts, r.NextPageToken, nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := map[string]any{"products": rows}
	if offers {
		product := id
		if product == "" {
			product = "-"
		}
		rows, err := pages(func(token string) ([]*pub.OneTimeProductOffer, string, error) {
			r, e := c.Publisher.Monetization.Onetimeproducts.PurchaseOptions.Offers.List(app, product, "-").PageToken(token).PageSize(1000).Context(ctx).Do()
			if e != nil {
				return nil, "", e
			}
			return r.OneTimeProductOffers, r.NextPageToken, nil
		})
		if err != nil {
			return nil, err
		}
		result["offers"] = rows
	}
	return result, nil
}
