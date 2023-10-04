#!/bin/sh

set -e
set -x

BASEDIR="${BASEDIR:-$(dirname -- "${0}")/../..}"
BASEDIR="$(readlink -f -- ${BASEDIR})"

SHELL="${SHELL:-"/bin/sh"}"

. ${BASEDIR}/scripts/travis/functions.sub

cd ${TESTSDIR}
ls -l
#init_mr_time
RTPPROXY_CMD="${RTPPROXY_BIN}" exec ${SHELL} basic_network
