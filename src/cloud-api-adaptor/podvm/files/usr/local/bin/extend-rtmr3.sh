#!/bin/bash

RTMR3="/sys/devices/virtual/misc/tdx_guest/measurements/rtmr3:sha384"

if [ -f $RTMR3 ]; then
    echo "Extend RTMR3 ..."
    awk '{printf "%s%032d", $0, 0}' /var/run/peerpod/initdata.digest | xxd -r -p > $RTMR3
fi
