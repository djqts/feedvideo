#!/bin/sh
# Age-based bucket expiry is paused until referenced objects are protected.
printf '%s\n' 'unavailable: lifecycle configuration NOT_RUN; target identity and protection of referenced objects are required.' >&2
exit 2
