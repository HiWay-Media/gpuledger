#!/bin/sh
# Stands in for nvidia-smi in tests: answers the CSV queries from the fixtures. The
# optional thermal queries answer under the current field names only, as a driver
# after the clocks_throttle_reasons → clocks_event_reasons rename does; any other
# query fails the way nvidia-smi fails for a field it does not know.
here=$(cd "$(dirname "$0")" && pwd)
case "$1" in
  --query-gpu=index,uuid,*) cat "$here/query-gpu.csv" ;;
  --query-gpu=index,temperature.gpu.tlimit) cat "$here/query-tlimit.csv" ;;
  --query-gpu=index,clocks_event_reasons.*) cat "$here/query-slowdown.csv" ;;
  --query-gpu=index,*) echo "Field \"${1#--query-gpu=index,}\" is not a valid field to query." >&2; exit 2 ;;
  --query-compute-apps=*) cat "$here/query-compute-apps.csv" ;;
  *) echo "fake nvidia-smi: unexpected args $*" >&2; exit 2 ;;
esac
