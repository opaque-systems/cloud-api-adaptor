#!/bin/bash

set -euxo pipefail

RTMR3="/sys/devices/virtual/misc/tdx_guest/measurements/rtmr3:sha384"
DIGEST_FILE="/var/run/peerpod/initdata.digest"
INIT_VAL=$(printf "%096d" 0)

if [ -f $RTMR3 ]; then
    CURRUENT_VAL=$(cat $RTMR3 | xxd -p -c 0)
    if [[ "$CURRUENT_VAL" == "$INIT_VAL" ]]; then
        echo "Extending RTMR3 ..."
        awk '{printf "%s%032d", $0, 0}' $DIGEST_FILE | xxd -r -p > $RTMR3
    else
        echo "RTMR3 has been extended, skip extending"
    fi
else
    echo "Non-TDX TEE is not supported yet."
fi
