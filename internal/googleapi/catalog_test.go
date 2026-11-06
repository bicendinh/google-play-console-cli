package googleapi_test

import (
	"context"
	"fmt"
	pub "google.golang.org/api/androidpublisher/v3"
	"net/http"
	"playops/internal/testutil"
	"strings"
	"testing"
)

func TestReadOnlyCatalogPaginationAndRegionalPrices(t *testing.T) {
	requests := 0
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Errorf("catalog write: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		page := r.URL.Query().Get("pageToken")
		switch {
		case strings.Contains(path, "/basePlans/"):
			if page == "" {
				fmt.Fprint(w, `{"subscriptionOffers":[{"productId":"premium","basePlanId":"yearly","offerId":"trial","regionalConfigs":[{"regionCode":"US","newSubscriberAvailability":true}]}],"nextPageToken":"offers-2"}`)
			} else {
				fmt.Fprint(w, `{"subscriptionOffers":[]}`)
			}
		case strings.HasSuffix(path, "/subscriptions"):
			if page == "" {
				fmt.Fprint(w, `{"subscriptions":[{"productId":"premium","basePlans":[{"basePlanId":"yearly","regionalConfigs":[{"regionCode":"US","price":{"currencyCode":"USD","units":"10"}},{"regionCode":"VN","price":{"currencyCode":"VND","units":"200000"}}]}]}],"nextPageToken":"subs-2"}`)
			} else {
				fmt.Fprint(w, `{"subscriptions":[{"productId":"other"}]}`)
			}
		case strings.Contains(path, "/purchaseOptions/"):
			fmt.Fprint(w, `{"oneTimeProductOffers":[{"productId":"coin_pack","offerId":"sale","purchaseOptionId":"buy"}]}`)
		case strings.HasSuffix(path, "/oneTimeProducts"):
			fmt.Fprint(w, `{"oneTimeProducts":[{"productId":"coin_pack","purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE"}]}]}`)
		case strings.HasSuffix(path, "/inappproducts"):
			if r.URL.Query().Get("token") == "" {
				fmt.Fprint(w, `{"inappproduct":[{"sku":"legacy"}],"tokenPagination":{"nextPageToken":"next"}}`)
			} else {
				fmt.Fprint(w, `{"inappproduct":[]}`)
			}
		default:
			t.Errorf("unexpected %s", path)
			http.Error(w, "unexpected", 500)
		}
	})
	subs, err := client.Subscriptions(context.Background(), "com.example.app", "", true)
	if err != nil {
		t.Fatal(err)
	}
	rows := subs["subscriptions"].([]*pub.Subscription)
	if len(rows) != 2 || len(subs["offers"].([]*pub.SubscriptionOffer)) != 1 {
		t.Fatal("pagination lost entries")
	}
	prices := rows[0].BasePlans[0].RegionalConfigs
	if prices[0].Price.CurrencyCode != "USD" || prices[1].Price.CurrencyCode != "VND" {
		t.Fatal("regional currencies were merged")
	}
	products, err := client.Products(context.Background(), "com.example.app", "", true, false)
	if err != nil || len(products["products"].([]*pub.OneTimeProduct)) != 1 {
		t.Fatalf("products: %+v %v", products, err)
	}
	legacy, err := client.Products(context.Background(), "com.example.app", "", false, true)
	if err != nil || len(legacy["products"].([]*pub.InAppProduct)) != 1 {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	if requests != 8 {
		t.Fatalf("unexpected pagination request count %d", requests)
	}
}
