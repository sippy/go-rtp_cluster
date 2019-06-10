// Copyright (c) 2003-2005 Maxim Sobolev <sobomax@gmail.com>
// Copyright (c) 2006-2019, Sippy Software, Inc., http://www.sippysoft.com
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
package main

import (
    "fmt"
    "math"
    "math/rand"
    "net"
    "net/url"
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "sippy"
    "sippy/cli"
    "sippy/log"
    "sippy/net"
    "sippy/time"
    "sippy/utils"

)

type NetworkTransport interface {
    Shutdown()
}

type broadcaster struct {
    bcount  int64
    ecount  int64
    nparts  int64
    results []string
    clim    sippy_cli.CLIManagerIface
    cmd     *sippy.Rtp_proxy_cmd
    sobj    *sippy.Rtpp_stats
}

func newBroadcaster(bcount int64, clim sippy_cli.CLIManagerIface, cmd *sippy.Rtp_proxy_cmd) *broadcaster {
    return &broadcaster{
        results : make([]string, 1000),
        bcount  : bcount,
        ecount  : bcount,
        nparts  : bcount,
        clim    : clim,
        cmd     : cmd,
    }
}

func (self *broadcaster) all_names() string {
    return strings.Join(self.sobj.AllNames(), "|")
}

func (self *broadcaster) dec_bcount() int64 {
    return atomic.AddInt64(&self.bcount, -1)
}

func (self *broadcaster) dec_ecount() int64 {
    return atomic.AddInt64(&self.ecount, -1)
}

type udpCLIM struct {
    cookie  string
    address *sippy_net.HostPort
    server  sippy_net.Transport
}

func newUdpCLIM(address *sippy_net.HostPort, cookie string, server sippy_net.Transport) *udpCLIM {
    return &udpCLIM{
        address : address,
        cookie  : cookie,
        server  : server,
    }
}

func (self *udpCLIM) Send(data string) {
    self.server.SendTo([]byte(self.cookie + " " + data), self.address)
}


func (self *udpCLIM) Close() {
    self.server = nil
}

func (self *udpCLIM) RemoteAddr() net.Addr {
    return nil
}

type rtp_cluster struct {
    logger              sippy_log.ErrorLogger
    lock                sync.Mutex
    address             string
    name                string
    active              []*rtp_cluster_member
    pending             []*rtp_cluster_member
    ccm                 NetworkTransport
    commands_inflight   map[string]bool
    l1rcache            map[string]string
    l2rcache            map[string]string
    cache_purge_el      *sippy.Timeout
    dnrelay             *DNRelay
    capacity_limit_soft bool
}

func NewRtp_cluster(global_config *mainConfig, name, protocol, address string, dnconfig *DisconnectNotify) (*rtp_cluster, error) {
    self := &rtp_cluster{
        active              : make([]*rtp_cluster_member, 0, 1000),
        pending             : make([]*rtp_cluster_member, 0, 1000),
        l1rcache            : make(map[string]string),
        l2rcache            : make(map[string]string),
        logger              : global_config.ErrorLogger(),
        name                : name,
        address             : address,
        commands_inflight   : make(map[string]bool),
    }
    if protocol != "unix" {
        host, port, err := net.SplitHostPort(address)
        if err != nil {
            return nil, err
        }
        uopts := sippy.NewUdpServerOpts(sippy_net.NewHostPort(host, port), self.up_command_udp)
        self.ccm, err = sippy.NewUdpServer(global_config, uopts)
        if err != nil {
fmt.Println(err.Error(), host, port)
            return nil, err
        }
    } else {
        var err error
        self.ccm, err = sippy_cli.NewCLIConnectionManagerUnix(self.up_command, address, global_config.sock_uid, global_config.sock_gid, self.logger)
        if err != nil {
            return nil, err
        }
    }
    self.cache_purge_el = sippy.NewInactiveTimeout(self.rCachePurge, nil, 10 * time.Second, -1, self.logger)
    self.cache_purge_el.SpreadRuns(0.1)
    self.cache_purge_el.Start()
    self.update_dnrelay(dnconfig)
    return self, nil
}

func (self *rtp_cluster) update_dnrelay(dnconfig *DisconnectNotify) {
    var allow_from []string
    var err error

    if self.dnrelay != nil {
        if dnconfig != nil && self.dnrelay.Cmpconfig(dnconfig) {
            return
        }
        allow_from = self.dnrelay.Get_allow_list()
        self.dnrelay.Shutdown()
        self.dnrelay = nil
    }

    if dnconfig == nil {
        return
    }

    self.dnrelay, err = NewDNRelay(dnconfig, self.logger)
    if err != nil {
        self.logger.Error("Cannot create DNRelay: " + err.Error())
        return
    }
    if allow_from != nil {
        self.dnrelay.Set_allow_list(allow_from)
    }
}

func (self *rtp_cluster) add_member(member *rtp_cluster_member) {
    member.on_state_change = self.rtpp_status_change
    member.Start()
    if member.IsOnline() {
        self.append_active(member)
    } else {
        self.append_pending(member)
    }
    if ! member.IsLocal() && self.dnrelay != nil {
        self.dnrelay.AppendAllowFrom(member.Address())
    }
}

func (self *rtp_cluster) append_active(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    self.active = append(self.active, rtpp)
}

func (self *rtp_cluster) append_pending(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    self.pending = append(self.pending, rtpp)
}

func (self *rtp_cluster) move_to_active(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    for i, v := range self.pending {
        if v == rtpp {
            self.pending = append(self.pending[:i], self.pending[i+1:]...)
            self.active = append(self.active, rtpp)
            break
        }
    }
}

func (self *rtp_cluster) move_to_pending(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    for i, v := range self.active {
        if v == rtpp {
            self.active = append(self.active[:i], self.active[i+1:]...)
            self.pending = append(self.pending, rtpp)
            break
        }
    }
}

func (self *rtp_cluster) remove_pending(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    for i, v := range self.pending {
        if v == rtpp {
            self.pending = append(self.pending[:i], self.pending[i+1:]...)
            return
        }
    }
}

func (self *rtp_cluster) remove_active(rtpp *rtp_cluster_member) {
    self.lock.Lock()
    defer self.lock.Unlock()
    for i, v := range self.active {
        if v == rtpp {
            self.active = append(self.active[:i], self.active[i+1:]...)
            return
        }
    }
}

func (self *rtp_cluster) is_in_active(rtpp *rtp_cluster_member) bool {
    // no need for locking as the array is a value
    for _, v := range self.active {
        if rtpp == v {
            return true
        }
    }
    return false
}

func (self *rtp_cluster) is_in_pending(rtpp *rtp_cluster_member) bool {
    // no need for locking as the array is a value
    for _, v := range self.pending {
        if rtpp == v {
            return true
        }
    }
    return false
}

func (self *rtp_cluster) rtpp_status_change(rtpp *rtp_cluster_member, online bool) {
    if online {
        self.move_to_active(rtpp)
    } else {
        self.move_to_pending(rtpp)
    }
}

func (self *rtp_cluster) bring_down(rtpp *rtp_cluster_member) {
    //print 'bring_down', self, rtpp
    if ! rtpp.IsLocal() && self.dnrelay != nil {
        ip, _, err := net.SplitHostPort(rtpp.Address().String())
        if err == nil {
            self.dnrelay.disallow_from(ip)
        }
    }
    if self.is_in_active(rtpp) {
        if len(rtpp.call_id_map) == 0 || rtpp.GetActiveSessions() <= 0 {
            self.remove_active(rtpp)
            rtpp.Shutdown()
            return
        }
        rtpp.status = RTPCM_STATUS_DRAINING
        rtpp.on_active_update = self.rtpp_active_change
        return
    }
    self.remove_pending(rtpp)
    rtpp.Shutdown()
}

func (self *rtp_cluster) rtpp_active_change(rtpp *rtp_cluster_member, active_sessions int64) {
    if rtpp.status == RTPCM_STATUS_DRAINING && (len(rtpp.call_id_map) == 0 || active_sessions == 0) {
        if self.is_in_pending(rtpp) {
            self.remove_pending(rtpp)
        } else {
            self.remove_active(rtpp)
        }
        rtpp.Shutdown()
    }
}

func (self *rtp_cluster) up_command_udp(data []byte, address *sippy_net.HostPort, server sippy_net.Transport, rtime *sippy_time.MonoTime) {
    splittedData := sippy_utils.FieldsN(string(data), 2)
    if len(splittedData) == 1 {
        return
    }
    cookie := splittedData[0]
    cmd := splittedData[1]
    self.lock.Lock()
    if _, ok := self.commands_inflight[cookie]; ok {
        self.lock.Unlock()
        return
    }
    cresp, ok := self.l1rcache[cookie]
    if !ok {
        cresp, ok = self.l2rcache[cookie]
        if ! ok {
            cresp = ""
        }
    }

    if cresp != "" {
        response := cookie + " " + cresp
        server.SendTo([]byte(response), address)
        self.logger.Debugf("Rtp_cluster.up_command_udp(): sending cached response %s to %s", response, address.String())
        self.lock.Unlock()
        return
    }

    self.commands_inflight[cookie] = true
    self.lock.Unlock()
    clim := newUdpCLIM(address, cookie, server)
    self.up_command(clim, cmd)
}

func (self *rtp_cluster) up_command(clim sippy_cli.CLIManagerIface, orig_cmd string) {
    var rtpp *rtp_cluster_member
    cmd, err := sippy.NewRtp_proxy_cmd(orig_cmd)
    if err != nil {
        self.logger.Debugf("Rtp_cluster.up_command(): error parsing cmd '%s': %s", orig_cmd, err.Error())
        return
    }
    response_handler := self.down_command

    if len(self.active) == 0 {
        self.down_command("E999", clim, cmd, nil)
        return
    }

    if strings.IndexByte("ULDPSRCQ", cmd.Type) != -1 {
        found := false
        for _, rtpp = range self.active {
            if rtpp.isYours(cmd.CallId){
                found = true
                break
            }
        }
        if ! found {
            rtpp = nil
        }
        new_session := false
        if cmd.Type == 'U' && cmd.ULOpts.ToTag == "" {
            new_session = true
        }
        if rtpp == nil && ! new_session {
            // Existing session, also check if it exists on any of the offline
            // members and try to relay it there, it makes no sense to broadcast
            // the call to every other node in that case.
            found = false
            for _, rtpp = range self.pending {
                if rtpp.isYours(cmd.CallId){
                    found = true
                    break
                }
            }
            if ! found {
                rtpp = nil
            }
        }
        if rtpp != nil && cmd.Type == 'D' {
            rtpp.unbind_session(cmd.CallId)
            if ! rtpp.IsOnline() {
                self.logger.Debug("Delete requesting to a (possibly) offline node "+ rtpp.name +", sending fake reply in the background")
                self.down_command("0", clim, cmd, nil)
                response_handler = self.ignore_response
            }
        }
        if rtpp == nil && new_session {
            // New Session
            rtpp = self.pick_proxy(cmd.CallId)
            if rtpp == nil {
                self.down_command("E998", clim, cmd, nil)
                return
            }
            rtpp.bind_session(cmd.CallId, cmd.Type)
        }
        if rtpp != nil && strings.IndexByte("UL", cmd.Type) != -1 && cmd.ULOpts.NotifySocket != "" {
            if rtpp.WdntSupported() && self.dnrelay != nil && ! rtpp.IsLocal() && strings.HasPrefix(cmd.ULOpts.NotifySocket, self.dnrelay.dest_sprefix) {
                pref_len := len(self.dnrelay.dest_sprefix)
                notify_tag, err := url.QueryUnescape(cmd.ULOpts.NotifyTag)
                if err != nil {
                    self.logger.Error("Error unescaping the notify_tag: " + err.Error())
                    return
                }
                dnstr := cmd.ULOpts.NotifySocket[pref_len:] + notify_tag
                cmd.ULOpts.NotifyTag = url.QueryEscape(dnstr)
                cmd.ULOpts.NotifySocket = "tcp:%%CC_SELF%%:" + self.dnrelay.in_address.Port.String()
                orig_cmd = cmd.String()
            } else if ! rtpp.IsLocal() {
                cmd.ULOpts.NotifyTag = ""
                cmd.ULOpts.NotifySocket = ""
                orig_cmd = cmd.String()
            }
        }
        if rtpp == nil {
            // Existing session we know nothing about
            if cmd.Type == 'U' {
                // Do a forced lookup
                //
                // Translation from Python note:
                //orig_cmd = "L" + cmd.ULOpts.Getstr(cmd.call_id, true)
                // the swaptags is true here but the swaptags in the original code is actually a no-op
                orig_cmd = "L" + cmd.ULOpts.Getstr(cmd.CallId)
            }
            active := []*rtp_cluster_member{}
            for _, x := range self.active {
                if x.IsOnline() {
                    active = append(active, x)
                }
            }
            br := newBroadcaster(int64(len(active)), clim, cmd)
            for _, rtpp = range active {
                out_cmd_s := orig_cmd
                if (cmd.Type == 'U' || cmd.Type == 'L') && rtpp.lan_address != "" {
                    out_cmd, err := sippy.NewRtp_proxy_cmd(orig_cmd)
                    if err != nil {
                        self.logger.Errorf("Error parsing cmd '%s': %s: ", orig_cmd, err.Error())
                        return
                    }
                    out_cmd.ULOpts.LocalIP = rtpp.lan_address
                    out_cmd.ULOpts.DestinationIP = ""
                    out_cmd_s = out_cmd.String()
                }
                rtpp.SendCommand(out_cmd_s, func(res string) { self.merge_results(res, br, rtpp) })
            }
            return
        }
    } else if cmd.Type == 'I' && cmd.CommandOpts == "b" {
        active := []*rtp_cluster_member{}
        for _, x := range self.active {
            if x.IsOnline() {
                active = append(active, x)
            }
        }
        var sessions_created, active_sessions, active_streams, preceived, ptransmitted int64
        for _, rtpp = range active {
            if rtpp.GetActiveSessions() <= 0 {
                // There might be some time between "online" and heartbeat reply,
                // when stats are still empty, or when proxy goes from offline
                // to online, skip it
                continue
            }
            sessions_created += rtpp.GetSessionsCreated()
            active_sessions += rtpp.GetActiveSessions()
            active_streams += rtpp.GetActiveStreams()
            preceived += rtpp.GetPReceived()
            ptransmitted += rtpp.GetPTransmitted()
        }
        reply := fmt.Sprintf("sessions created: %d\nactive sessions: %d\nactive streams: %d\npackets received: %d\npackets transmitted: %d", sessions_created, active_sessions, active_streams, preceived, ptransmitted)
        self.down_command(reply, clim, cmd, nil)
        return
    } else if cmd.Type == 'G' {
        active := []*rtp_cluster_member{}
        for _, x := range self.active {
            if x.IsOnline() {
                active = append(active, x)
            }
        }
        br := newBroadcaster(int64(len(active)), clim, cmd)
        br.sobj = sippy.NewRtpp_stats(strings.Fields(cmd.Args))
        if cmd.CommandOpts != "" && strings.ToLower(cmd.CommandOpts) == "v" {
            cmd.CommandOpts = ""
            br.sobj.Verbose = true
        }
        cmd.Nretr = 0
        for _, rtpp = range active {
            rtpp.SendCommand(cmd.String(), func(res string) { self.merge_stats_results(res, br, rtpp) })
        }
        return
    } else {
        rtpp = self.active[0]
        //print 'up', cmd
    }
    //print 'rtpp.send_command'
    var out_cmd string
    if (cmd.Type == 'U' || cmd.Type == 'L') && rtpp.lan_address != "" {
        cmd, err := sippy.NewRtp_proxy_cmd(orig_cmd)
        if err != nil {
            self.logger.Errorf("Error parsing cmd '%s': %s: ", orig_cmd, err.Error())
            return
        }
        cmd.ULOpts.LocalIP = rtpp.lan_address
        cmd.ULOpts.DestinationIP = ""
        out_cmd = cmd.String()
    } else {
        out_cmd = orig_cmd
    }
    rtpp.SendCommand(out_cmd, func (res string) { response_handler(res, clim, cmd, rtpp)})
}

func (self *rtp_cluster) rCachePurge() {
    self.lock.Lock()
    defer self.lock.Unlock()
    self.l2rcache = self.l1rcache
    self.l1rcache = make(map[string]string)
}

func (self *rtp_cluster) down_command(result string, clim sippy_cli.CLIManagerIface, cmd *sippy.Rtp_proxy_cmd, rtpp *rtp_cluster_member) {
    if udpclim, ok := clim.(*udpCLIM); ok {
        self.lock.Lock()
        if _, ok = self.commands_inflight[udpclim.cookie]; ok {
            delete(self.commands_inflight, udpclim.cookie)
        }
        self.lock.Unlock()
    }
    //print 'down', result
    if result == "" {
        result = "E997"
    } else if strings.IndexByte("UL", cmd.Type) != -1 && strings.ToUpper(result[:1]) != "E" && rtpp.wan_address != "" {
        //print 'down', cmd.ul_opts.destination_ip, rtpp.wan_address
        req_dip := cmd.ULOpts.DestinationIP
        req_lip := cmd.ULOpts.LocalIP
        result_parts := strings.Fields(strings.TrimSpace(result))
        if result_parts[0] != "0" && req_dip != "" && ! is_dst_local(req_dip) && req_lip != rtpp.lan_address {
            result = result_parts[0] + " " + rtpp.wan_address
        } else if result_parts[0] != "0" && req_lip == "" {
            result = result_parts[0] + " " + rtpp.wan_address
        }
    //    result = '%s %s' % (result_parts[0], '192.168.1.22')
    //print 'down clim.send', result
    }
    response := result + "\n"
    clim.Send(response)
    if udpclim, ok := clim.(*udpCLIM); ok {
        self.lock.Lock()
        self.l1rcache[udpclim.cookie] = response
        self.lock.Unlock()
    }
    clim.Close()
}

func (self *rtp_cluster) ignore_response(result string, clim sippy_cli.CLIManagerIface, cmd *sippy.Rtp_proxy_cmd, rtpp *rtp_cluster_member) {
    self.logger.Debugf("Got delayed response from node \"%s\" to already completed request, ignoring: \"%s\"", rtpp.name, result)
}

func (self *rtp_cluster) pick_proxy(call_id string) *rtp_cluster_member {
    type rtpp_with_weight struct {
        rtpp    *rtp_cluster_member
        weight  float64
    }
    active := []rtpp_with_weight{}
    available := []rtpp_with_weight{}
    total_weight := float64(0)
    for _, rtpp := range self.active {
        if rtpp.status == RTPCM_STATUS_ACTIVE && rtpp.IsOnline() {
            it := rtpp_with_weight{ rtpp, float64(rtpp.weight) * (1 - rtpp.get_caputil()) }
            active = append(active, it)
            if it.weight > 0 {
                available = append(available, it)
                total_weight += it.weight
            }
        }
    }
    if len(available) > 0 {
        var it rtpp_with_weight
        // Normal case, there are some proxies that are loaded below their capacities
        thr_weight := math.Remainder(rand.Float64() * total_weight, total_weight)
        //print total_weight, thr_weight
        for _, it = range available {
            thr_weight -= it.weight
            if thr_weight < 0 {
                break
            }
        }
        //print 'pick_proxyNG: picked up %s for the call %s (normal)' % (rtpp.name, call_id)
        return it.rtpp
    } else if len(active) > 0 && self.capacity_limit_soft {
        max_it := active[0]
        for _, it := range active[1:] {
            if it.weight > max_it.weight {
                max_it = it
            }
        }
        //print 'pick_proxyNG: picked up %s for the call %s (overload)' % (max_rtpp.name, call_id)
        return max_it.rtpp
    }
    self.logger.Debug("pick_proxyNG: OUCH, no proxies to pickup from for the call " + call_id)
    return nil
}

func is_dst_local(destination_ip string) bool {
    //if destination_ip == '192.168.22.11':
    //    return True
    return false
}

func (self *rtp_cluster) Shutdown() {
    for _, rtpp := range append(self.active, self.pending...) {
        rtpp.Shutdown()
    }
    if self.ccm != nil {
        self.ccm.Shutdown()
    }
    if self.cache_purge_el != nil {
        self.cache_purge_el.Cancel()
    }
    self.active = nil
    self.pending = nil
    self.ccm = nil
    self.cache_purge_el = nil
    if self.dnrelay != nil {
        self.dnrelay.Shutdown()
    }
}

func (self *rtp_cluster) rtpp_by_name(name string) (*rtp_cluster_member, int) {
    for idx, rtpp := range append(self.active, self.pending...) {
        if rtpp.name == name {
            return rtpp, idx
        }
    }
    return nil, -1
}

func (self *rtp_cluster) all_members() []*rtp_cluster_member {
    return append(self.active, self.pending...)
}

func (self *rtp_cluster) merge_results(result string, br *broadcaster, rtpp *rtp_cluster_member) {
    if result == "" {
        result = "E996"
    }
    if br != nil && strings.ToUpper(result)[0] != 'E' && ! ((br.cmd.Type == 'U' || br.cmd.Type == 'L') && result == "0") {
        br.results = append(br.results, result)
    }
    bcount := br.dec_bcount()
    if bcount > 0 {
        // More results to come
        return
    }
    if len(br.results) == 1 {
        rtpp.bind_session(br.cmd.CallId, br.cmd.Type)
        self.down_command(br.results[0], br.clim, br.cmd, rtpp)
    } else {
        // No results or more than one proxy returns positive
        // XXX: more than one result can probably be handled
        if br.cmd.Type == 'U' || br.cmd.Type == 'L' {
            self.down_command("0", br.clim, br.cmd, rtpp)
        } else {
            self.down_command("E995", br.clim, br.cmd, rtpp)
        }
    }
}

func (self *rtp_cluster) merge_stats_results(result string, br *broadcaster, rtpp *rtp_cluster_member) {
    //print 'merge_stats_results, result', result
    if result == "" {
        var ok bool
        if result, ok = rtpp.stats_cache_get(br.all_names()); !ok {
            result = "E994"
        }
        self.logger.Debugf("merge_stats_results: node \"%s\": getting from the cache \"%s\"", rtpp.name, result)
    } else if strings.ToUpper(result)[0] != 'E' {
        rtpp.stats_cache_set(br.all_names(), result)
    }
    ecount := br.ecount
    if br != nil && strings.ToUpper(result)[0] != 'E' {
        if br.sobj.ParseAndAdd(result) == nil {
            ecount = br.dec_ecount()
        }
    }
    bcount := br.dec_bcount()
    if bcount > 0 {
        // More results to come
        return
    }
    //print 'merge_stats_results, br.sobj', br.sobj
    rval := "E993"
    if ecount != br.nparts {
        rval = br.sobj.String()
    }
    self.down_command(rval, br.clim, br.cmd, rtpp)
}
