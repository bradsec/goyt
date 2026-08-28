#!/usr/bin/env bash

set -euo pipefail

base_url=${TEST_BASE_URL:-http://localhost:3000}

if ! curl --fail --silent --show-error "$base_url/health" >/dev/null; then
  echo "goyt is not reachable at $base_url; start the server or set TEST_BASE_URL" >&2
  exit 1
fi

npm run test-ui
