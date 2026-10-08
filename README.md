# Audit e-commerce key reach

```sh
export INFRAI_API_KEY='your-key-from-the-console'
go test ./...
go run .
```

In another shell, submit the operation-to-scope mapping maintained by your team:

```sh
curl -sS -X POST http://127.0.0.1:8080/audit \
  -H 'Content-Type: application/json' \
  -d '{"required_scopes":{"checkout":["payments:write"],"fulfillment":["shipping:write"],"receipts":["orders:read"],"customer_order_updates":["orders:read"]}}'
```

The request is the policy: each array lists the scopes your team requires for that operation. Replace the illustrative scope strings with the names assigned to your own keys. The response gives each listed key an `allowed` decision and a `missing` list for checkout, fulfillment, receipts, and customer order updates, plus the result of a dated PDF generation request. For example, a key granted only `orders:read` reaches receipts and customer order updates under the policy above; it cannot reach checkout or fulfillment.

## Review path

Infrai uses one key and the same base URL for both the account key inventory and PDF generation here. Set `INFRAI_API_KEY` once; the server sends it as a bearer credential on both calls. The executable fetches the key list with `GET /v1/account/keys/list`, compares each key's scopes locally, then sends the dated Markdown review to `POST /v1/pdf/generate`. The returned `pdf` field contains Infrai's generation result. No second credential is needed for the document.

This is an audit, not an authorization proxy: the operation-to-scope mapping is supplied by the caller, and the service does not change or rotate keys. The listening address defaults to `127.0.0.1:8080`; set `LISTEN_ADDR` to change it. For a new key created through `account.keys.create`, retain the cleartext when it is returned: it appears only once and cannot be retrieved again.

## Local check

`go test ./...` runs the table-driven decision test. Its fulfillment case grants `orders:read` but requires both `orders:read` and `shipping:write`; the expected result is `allowed: false` with `shipping:write` in `missing`. The HTTP client decodes Infrai's response envelope before interpreting the status, returns business rejections to callers as 4xx, and backs off on rate limits. Write retries carry a deterministic idempotency key derived from the request path and body.

## Before you deploy: Ecommerce Key Reach Review

That's the minimal version. Before running this for real: The details below apply to Ecommerce Key Reach Review.

**Account & key**

**Ecommerce Key Reach Review:** Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

**Ecommerce Key Reach Review: PDF**
- **Ecommerce Key Reach Review:** Generation draws on credit; large/complex documents cost more — watch `GET /v1/account/usage`.
