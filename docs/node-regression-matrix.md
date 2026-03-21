# Node Regression Matrix

This matrix is used for node-side parity validation after migration.

## Scope

- runtime sync and reconcile
- traffic/aliveip/detectlog reporting
- SSR/SS behavior with multi-user modes

## Matrix

| Case | Driver | MUOnly | is_multi_user | Rule type | Expected |
| --- | --- | ---: | ---: | --- | --- |
| A1 | ssr | 0 | 0 | text | traffic + aliveip + detectlog normal |
| A2 | ssr | 1 | 1 | text | effective port = port + offset |
| A3 | ssr | -1 | 1 | text | user filtered by MUOnly |
| B1 | ssr | 0 | 1 | hex | MU user upload/download aggregated |
| B2 | ssr | 0 | 1 | text+hex | both rule buckets hit and upload |
| C1 | ss | 0 | 0 | text | first-packet detect hit |
| C2 | ss | 0 | 0 | none | traffic + aliveip normal |
| D1 | ssr | 0 | 1 | none | MU host mismatch rejected |
| D2 | ssr | 0 | 1 | none | MU host match accepted |
| E1 | ss/ssr | 0 | any | none | unsupported cipher: skip/fail follows config |

## Runtime Config Coverage

- `runtime.on_unsupported_cipher = skip|fail`
- `runtime.switchrule.enabled = true/false`
- `runtime.switchrule.mode = expr`
- `runtime.dns_prefer_ipv4 = true/false`
- `runtime.dns_resolver = "" / "1.1.1.1:53"`

## Operational Checks

- `runtime sync reconciled` log has `input_users/effective_users/unsupported_skipped`
- switchrule log has `before/after/filtered`
- node status post continues even partial push failures
