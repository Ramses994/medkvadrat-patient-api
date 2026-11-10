#!/usr/bin/env bash
# Dev-only smoke: OTP → /api/me + catalog reads. Does not book or cancel.
# Requires: curl, jq. Tested on Linux and Git Bash (GNU or BSD date for slot window).
set -euo pipefail

usage() {
  echo "Usage: $0 BASE_URL PHONE" >&2
  echo "Example: $0 http://localhost:8082 79935950695" >&2
  exit 1
}

[[ ${1-} && ${2-} ]] || usage
BASE_URL="${1%/}"
PHONE="$2"

if ! command -v curl >/dev/null || ! command -v jq >/dev/null; then
  echo "This script needs curl and jq in PATH" >&2
  exit 1
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/mekv-smoke.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
BODY="${TMP}/body"

if date -u -d "today" +%F >/dev/null 2>&1; then
  DF=$(date -u -d "today" +%F)
  DT1=$(date -u -d "today + 1 day" +%F)
else
  DF=$(date -u +%F)
  DT1=$(date -u -v+1d +%F 2>/dev/null || date -u +%F)
fi

ok=0
fail=0
failed=()
step=0
LAST_CODE=

do_curl() {
  local name="$1"
  local method="$2"
  local url="$3"
  shift 3
  step=$((step + 1))
  LAST_CODE=$(
    curl -sS -o "$BODY" -w '%{http_code}' -X "$method" \
      "$@" \
      "$url" || true
  )
  LAST_CODE=$(printf '%s' "$LAST_CODE" | tr -d '\n')
  local preview
  preview=$(
    head -c 500 <"$BODY" 2>/dev/null | sed -E 's/"(access|refresh)":"[^"]{10,}/"\1":"<REDACTED>/g' || true
  )
  echo "----- step ${step}: ${name} -----"
  echo "  ${method} ${url}"
  echo "  status: ${LAST_CODE}"
  echo "  body (first 500 bytes): ${preview}"
  echo
  if [[ "$LAST_CODE" =~ ^2 ]]; then
    ok=$((ok + 1))
  else
    fail=$((fail + 1))
    failed+=("${name} (${LAST_CODE})")
  fi
}

# 1) OTP request
do_curl "auth otp request" "POST" "${BASE_URL}/api/auth/otp/request" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg p "$PHONE" '{phone: $p}')"
[[ "$LAST_CODE" =~ ^2 ]] || { echo "auth otp request failed" >&2; exit 1; }
RID=$(jq -r '.data.request_id // empty' <"$BODY")

# 2) OTP verify (dev code)
do_curl "auth otp verify" "POST" "${BASE_URL}/api/auth/otp/verify" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg r "$RID" '{request_id: $r, code: "000000"}')"
[[ "$LAST_CODE" =~ ^2 ]] || { echo "auth otp verify failed" >&2; exit 1; }
ACCESS=$(jq -r '.data.access // empty' <"$BODY")

# 2b) Multi-patient: pick first candidate
if [[ -z "$ACCESS" || "$ACCESS" == "null" ]]; then
  CAND=$(jq -r '((.data.patient_candidates) // [])[0].patient_id // empty' <"$BODY")
  if [[ -z "$CAND" || "$CAND" == "null" ]]; then
    echo "No access and no patient_candidates" >&2
    cat "$BODY" >&2
    exit 1
  fi
  do_curl "auth otp select-patient" "POST" "${BASE_URL}/api/auth/otp/select-patient" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg r "$RID" --argjson p "$CAND" '{request_id: $r, patient_id: $p}')"
  [[ "$LAST_CODE" =~ ^2 ]] || { echo "select-patient failed" >&2; exit 1; }
  ACCESS=$(jq -r '.data.access // empty' <"$BODY")
fi

if [[ -z "$ACCESS" || "$ACCESS" == "null" ]]; then
  echo "No JWT access in auth responses" >&2
  exit 1
fi
AUTH=(-H "Authorization: Bearer ${ACCESS}")

# 3) Me profile
do_curl "me profile" "GET" "${BASE_URL}/api/me/profile" "${AUTH[@]}"
[[ "$LAST_CODE" =~ ^2 ]] || exit 1

# 4) Appointments
do_curl "me appointments upcoming" "GET" "${BASE_URL}/api/me/appointments?status=upcoming" "${AUTH[@]}"
[[ "$LAST_CODE" =~ ^2 ]] || exit 1

# 5) Catalog doctors
do_curl "catalog doctors" "GET" "${BASE_URL}/api/catalog/doctors" "${AUTH[@]}"
[[ "$LAST_CODE" =~ ^2 ]] || exit 1
DOCTOR_IDS=$(jq -r '.data[]? | select((.full_name // "") | test("test|expert|docexpert"; "i") | not) | .doctor_id // empty' <"$BODY" | awk 'NF')
if [[ -z "${DOCTOR_IDS}" ]]; then
  echo "catalog/doctors: empty list (or all filtered out)" >&2
  exit 1
fi

# 6) Booking options and slots. Read only: this script does not book.
do_curl "booking options" "GET" "${BASE_URL}/api/catalog/booking-options" "${AUTH[@]}"
[[ "$LAST_CODE" =~ ^2 ]] || exit 1
SUBJ=$(jq -r '.data[0].pl_subj_id // empty' <"$BODY")
EXAM=$(jq -r '.data[0].pl_exam_id // empty' <"$BODY")
if [[ -n "$SUBJ" && "$SUBJ" != "null" && -n "$EXAM" && "$EXAM" != "null" ]]; then
  do_curl "catalog slots" "GET" \
    "${BASE_URL}/api/catalog/slots?pl_subj_id=${SUBJ}&pl_exam_id=${EXAM}&date_from=${DF}&date_to=${DT1}" \
    "${AUTH[@]}"
  [[ "$LAST_CODE" =~ ^2 ]] || exit 1
else
  echo "booking-options empty; slots skipped"
fi

echo "========== SUMMARY =========="
if (( fail == 0 )); then
  echo "OK: ${ok} / FAIL: ${fail}"
else
  echo "OK: ${ok} / FAIL: ${fail}"
  echo "Failed:"
  for f in "${failed[@]}"; do
    echo "  - $f"
  done
  exit 1
fi
