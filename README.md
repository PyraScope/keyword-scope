# Keyword Prometheus Exporter

A small Prometheus exporter that checks a set of URLs for keywords defined in a YAML config and exposes a metric indicating whether each keyword was found.

## Metrics

- `keyword_found{name,url,keyword}`: `1` if keyword found, `0` if not found
- `keyword_check_errors_total{name,url}`: count of errors while checking a target
- `keyword_last_check_timestamp_seconds{name,url}`: unix timestamp of last check

## Config

Example `config.yaml`:

```yaml
interval_seconds: 30

targets:
  - name: example
    url: "https://example.com"
    keyword: "Example Domain"
    case_insensitive: false
    timeout_seconds: 10
    max_bytes: 5242880
```

## Run

```bash
go run . -config config.yaml -listen :9105
```

Then scrape `http://localhost:9105/metrics` from Prometheus.

## Notes

- `interval_seconds` defaults to 30 seconds if not set or set to `0`.
- `timeout_seconds` defaults to 10 seconds; `max_bytes` defaults to 5MB.
- One goroutine per target performs checks on the interval.
