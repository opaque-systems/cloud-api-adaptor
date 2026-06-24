#!/usr/bin/env bash

set -euxo pipefail

RTMR3='/sys/devices/virtual/misc/tdx_guest/measurements/rtmr3:sha384'

if [[ ! -f ${RTMR3} ]]; then
	echo "${RTMR3} does not exist"
	echo 'Non-TDX TEE is not supported yet'
	exit 0
fi

CURRENT_VAL=$(xxd -p -c 0 "${RTMR3}")
INIT_VAL=$(printf "%096d" 0)

if [[ ${CURRENT_VAL} != "${INIT_VAL}" ]]; then
	echo "RTMR3 has been extended already, skip extending; current value: ${CURRENT_VAL}"
	exit 0
fi

DIGEST_FILE='/var/run/peerpod/initdata.digest'

echo 'Extending RTMR3 ...'
awk '{printf "%s%032d", $0, 0}' "${DIGEST_FILE}" | xxd -r -p -c 0 >"${RTMR3}"

NEW_VAL=$(xxd -p -c 0 "${RTMR3}")
echo "RTMR3 extension successful; new value: ${NEW_VAL}"
