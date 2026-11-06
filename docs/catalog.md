# Read-only catalog

```sh
playops subscriptions list
playops subscriptions get --product-id premium
playops products list
playops products get --product-id coin_pack
playops products list --legacy
```

These commands use GET requests only and never open an edit. Subscriptions use
the modern subscription/base-plan/offer APIs. Products default to the modern
one-time-products/purchase-options/offers APIs. `--legacy` explicitly reads the
older `inappproducts` catalog and has no offers request. There is no automatic
fallback to a different API if permissions or a catalog read fails.

Every list follows pagination, including offers. `--product-id` can also filter
list to a single product. `--include-offers=false` skips separate offer requests.
Results preserve Google's regional configuration, money units/nanos/currency,
state, base plan or purchase option IDs, offer phases and targeting. They do not
combine prices across regions or infer customer-specific trial eligibility.

Both `list` and `get` return arrays under `subscriptions` or `products`, with a
separate `offers` array when requested and supported. Google's typed resource
fields are preserved; clients should handle new optional fields and enum values.
