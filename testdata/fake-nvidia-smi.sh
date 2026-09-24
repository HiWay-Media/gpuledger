#!/bin/sh
# Stands in for nvidia-smi in tests: answers the two CSV queries from the fixtures.
here=$(cd "$(dirname "$0")" && pwd)
case "$1" in
  --query-gpu=*) cat "$here/query-gpu.csv" ;;
  --query-compute-apps=*) cat "$here/query-compute-apps.csv" ;;
  *) echo "fake nvidia-smi: unexpected args $*" >&2; exit 2 ;;
esac
