#!/usr/local/bin/python
# Copyright (c) 2006-2019, Sippy Software, Inc., http://www.sippysoft.com
# All rights reserved.
#
# Redistribution and use in source and binary forms, with or without
# modification, are permitted provided that the following conditions are met:
#
# 1. Redistributions of source code must retain the above copyright notice, this
#    list of conditions and the following disclaimer.
#
# 2. Redistributions in binary form must reproduce the above copyright notice,
#    this list of conditions and the following disclaimer in the documentation
#    and/or other materials provided with the distribution.
#
# THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
# AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
# IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
# DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
# FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
# DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
# SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
# CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
# OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
# OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

import socket
import sys
from hashlib import md5
from random import random
from time import time

def process_udp(addr):
    host, port = addr.rsplit(':', 1)
    port = int(port)
    ai = socket.getaddrinfo(host, port)
    family = ai[0][0]
    dst = ai[0][4]
    r = str(random())
    t = time()

    s = socket.socket(family, socket.SOCK_DGRAM)
    s.settimeout(1)

    for l in sys.stdin.readlines():
        l = l.strip()
        cookie = md5(r + str(t)).hexdigest()
        t += 1
        s.sendto(cookie + " " + l, dst)
        res = s.recv(256)
        print res.split(None, 1)[1].rstrip()

def usage():
    print('usage: rtp_proxy_client.py DESTINATION')
    sys.exit()

if __name__ == '__main__':
    if len(sys.argv) != 2:
        usage()

    addr = sys.argv[1]
    if addr.startswith("udp:"):
        process_udp(addr[4:])
