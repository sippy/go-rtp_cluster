// Copyright (c) 2009-2019 Sippy Software, Inc. All rights reserved.
//
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without modification,
// are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this
// list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice,
// this list of conditions and the following disclaimer in the documentation and/or
// other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
// ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR
// ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
// (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
// LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
// ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package main

import (
    "net"
    "strings"
    "sync"
    "time"

    "sippy"
    "sippy/conf"
    "sippy/log"
    "sippy/math"
    "sippy/net"
)

type rtp_cluster_member_status int

const (
    RTPCM_STATUS_ACTIVE = rtp_cluster_member_status(iota)
    RTPCM_STATUS_SUSPENDED
    RTPCM_STATUS_DRAINING
)

func RTPCMStatusFromString(s string) rtp_cluster_member_status {
    switch strings.ToUpper(s) {
    case "ACTIVE":
        return RTPCM_STATUS_ACTIVE
    case "SUSPENDED":
        return RTPCM_STATUS_SUSPENDED
    case "DRAINING":
        return RTPCM_STATUS_DRAINING
    }
    return RTPCM_STATUS_SUSPENDED
}

func (self rtp_cluster_member_status) String() string {
    switch self {
    case RTPCM_STATUS_ACTIVE:
        return "ACTIVE"
    case RTPCM_STATUS_SUSPENDED:
        return "SUSPENDED"
    case RTPCM_STATUS_DRAINING:
        return "DRAINING"
    }
    return "UNKNOWN"
}

type rtp_cluster_member struct {
    sippy.Rtp_proxy_client_base
    name                string
    status              rtp_cluster_member_status
    capacity            int
    weight              int
    wan_address         string
    lan_address         string
    call_id_map         map[string]bool
    call_id_map_old     map[string]bool
    call_id_map_lock    sync.Mutex
    on_state_change     func(*rtp_cluster_member, bool)
    on_active_update    func(*rtp_cluster_member, int64)
    timer               *sippy.Timeout
    logger              sippy_log.ErrorLogger
    asess_filtered      sippy_math.RecFilter
    cmd_out_address     *sippy_net.HostPort
    stats_cache         map[string]string
    lock                sync.Mutex
}

func NewRtp_cluster_member(name string, global_config sippy_conf.Config, protocol, address string, cmd_out_address *sippy_net.HostPort) *rtp_cluster_member {

    logger := global_config.ErrorLogger()
    opts, err := sippy.NewRtpProxyClientOpts(protocol + ":" + address, cmd_out_address, global_config, logger)
    if err != nil {
        println("Can't initialize rtpproxy client: " + err.Error())
        return nil
    }
    self := &rtp_cluster_member {
        name            : name,
        status          : RTPCM_STATUS_ACTIVE,
        capacity        : 4000,
        weight          : 100,
        call_id_map     : make(map[string]bool),
        call_id_map_old : make(map[string]bool),
        stats_cache     : make(map[string]string),
        asess_filtered  : sippy_math.NewRecFilter(0.5, 0.0),
        logger          : logger,
        cmd_out_address : cmd_out_address,
    }
    super := sippy.NewRtp_proxy_client_base(self, opts)
    self.Rtp_proxy_client_base = *super // this operation MUST be done before the call to Start()
    self.timer = sippy.NewInactiveTimeout(self.call_id_map_aging, /*sync.Locker*/nil, 600 * time.Second, -1, self.logger)
    self.timer.SpreadRuns(0.1)
    return self
}

func (self *rtp_cluster_member) Start() error {
    err := self.Rtp_proxy_client_base.Start()
    if err != nil {
        return err
    }
    self.timer.Start()
    return nil
}

func (self *rtp_cluster_member) stats_cache_get(key string) (string, bool) {
    self.lock.Lock()
    defer self.lock.Unlock()
    res, ok := self.stats_cache[key]
    return res, ok
}

func (self *rtp_cluster_member) stats_cache_set(key, val string) {
    self.lock.Lock()
    defer self.lock.Unlock()
    self.stats_cache[key] = val
}

// if the call_id is present in either call_id_map or call_id_map_old
// it brings the call_id to the beginning of the list and return true
func (self *rtp_cluster_member) isYours(call_id string) bool{
    self.call_id_map_lock.Lock()
    defer self.call_id_map_lock.Unlock()
    _, ok := self.call_id_map[call_id]
    if ok {
        return true
    }
    _, ok = self.call_id_map_old[call_id]
    if ok {
        self.call_id_map[call_id] = true
        delete(self.call_id_map_old, call_id)
    }
    return ok
}

func (self * rtp_cluster_member) bind_session(call_id string, cmd_type byte) {
    self.call_id_map_lock.Lock()
    defer self.call_id_map_lock.Unlock()
    if cmd_type == 'D' {
        self.call_id_map[call_id] = true
    } else {
        self.call_id_map_old[call_id] = true
    }
}

// remove the call_id from the call_id_map and insert it to the beginning of the 
// call_id_map_old
func (self * rtp_cluster_member) unbind_session(call_id string) {
    self.call_id_map_lock.Lock()
    defer self.call_id_map_lock.Unlock()

    delete(self.call_id_map, call_id)
    self.call_id_map_old[call_id] = true
}

func (self *rtp_cluster_member) GoOnline() {
    online_pre := self.IsOnline()
    self.Rtp_proxy_client_base.GoOnline()

    if online_pre || ! self.IsOnline() {
        return
    }

    self.logger.Debugf("Rtpproxy %s has changed status from offline to online", self.name)
    if self.on_state_change != nil {
        self.on_state_change(self, true)
    }
}

func (self *rtp_cluster_member) GoOffline() {
    if self.IsOnline() {
        self.logger.Debugf("RTPProxy %s has changed status from online to offline", self.name)
        self.lock.Lock()
        self.stats_cache = make(map[string]string)
        self.lock.Unlock()
        if self.on_state_change != nil {
            self.on_state_change(self, false)
        }
    }
    self.Rtp_proxy_client_base.GoOffline()
}

func (self *rtp_cluster_member) UpdateActive(active_sessions, sessions_created, active_streams, preceived, ptransmitted int64) {
    self.asess_filtered.Apply(float64(active_sessions))
    if self.GetActiveSessions() != active_sessions && self.on_active_update != nil {
        self.on_active_update(self, active_sessions)
    }
    self.Rtp_proxy_client_base.UpdateActive(active_sessions, sessions_created, active_streams, preceived, ptransmitted)
}

func (self *rtp_cluster_member) call_id_map_aging() {
    if self.IsShutDown() {
       self.timer.Cancel()
       return
    }
    self.call_id_map_lock.Lock()
    defer self.call_id_map_lock.Unlock()
    if len(self.call_id_map) < 1000 {
        // Do not age if there are less than 1000 calls in the list
        self.call_id_map_old = make(map[string]bool)
        return
    }
    self.call_id_map_old = self.call_id_map
    self.call_id_map = make(map[string]bool)
}

func (self *rtp_cluster_member) get_caputil() float64 {
    return (self.asess_filtered.GetLastval() / float64(self.capacity))
}

func (self *rtp_cluster_member) Reconnect(address net.Addr, bind_address *sippy_net.HostPort) {
    if self.cmd_out_address != nil {
        bind_address = self.cmd_out_address
    }
    self.Rtp_proxy_client_base.Reconnect(address, bind_address)
}
