#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

echo "=== vet ==="
go vet ./...

echo "=== test ==="
go test ./... -count=1

echo "=== build ==="
go build -o /dev/null ./cmd/tq/

echo "=== all checks passed ==="
