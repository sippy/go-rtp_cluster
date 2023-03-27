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
    "fmt"
    "io/ioutil"
    "net"
    "sort"
    "strconv"
    "strings"

    "github.com/sippy/go-b2bua/sippy/cli"
    "github.com/sippy/go-b2bua/sippy/log"
    "github.com/sippy/go-b2bua/sippy/net"
    "github.com/sippy/go-b2bua/sippy/utils"
)

type Rtp_cluster_cli_iface interface {
    append_cluster(*rtp_cluster)
    clusters() []*rtp_cluster
}

type Rtp_cluster_cli struct {
    ccm             *sippy_cli.CLIConnectionManager
    rtp_clusters    []*rtp_cluster
    global_config   *mainConfig
    logger          sippy_log.ErrorLogger
}

func NewRtp_cluster_cli(global_config *mainConfig, address string) (*Rtp_cluster_cli, error) {
    var err error
    self := &Rtp_cluster_cli {
        global_config   : global_config,
        logger          : global_config.ErrorLogger(),
        rtp_clusters    : make([]*rtp_cluster, 0),
    }
    self.ccm, err = sippy_cli.NewCLIConnectionManagerUnix(self.receive_command, address, global_config.sock_uid, global_config.sock_gid, self.logger)
    if err != nil {
        return nil, err
    }
    self.ccm.Start()
    return self, nil
}

func (self *Rtp_cluster_cli) receive_command(clim sippy_cli.CLIManagerIface, cmd string) {
    switch strings.SplitN(strings.TrimSpace(cmd), " ", 2)[0] {
    case "ls":
        parts := sippy_utils.FieldsN(cmd, 2)
        if len(parts) == 1 {
            idx := 0
            for _, rtp_cluster := range self.rtp_clusters {
                nonline := len(rtp_cluster.active)
                nsuspended := 0
                ndraining := 0
                for _, x := range rtp_cluster.active {
                    if x.status == RTPCM_STATUS_SUSPENDED {
                        nsuspended += 1
                    }
                }
                for _, x := range rtp_cluster.active {
                    if x.status == RTPCM_STATUS_DRAINING {
                        ndraining += 1
                    }
                }
                if idx > 0 {
                    clim.Send("\n")
                }
                clim.Send(fmt.Sprintf("Cluster: #%d\n", idx))
                clim.Send(fmt.Sprintf("    name = %s\n", rtp_cluster.name))
                clim.Send(fmt.Sprintf("    address = %s\n", string(rtp_cluster.address)))
                clim.Send(fmt.Sprintf("    online members = %d (%d active, %d suspended, %d draining)\n", nonline, nonline - nsuspended - ndraining, nsuspended, ndraining))
                clim.Send(fmt.Sprintf("    offline members = %d\n", len(rtp_cluster.pending)))
                idx += 1
            }
        } else {
            rtp_cluster, idx := self.cluster_by_name(parts[1])
            if rtp_cluster == nil {
                clim.Send(fmt.Sprintf("ERROR: %s: cluster not found\n", parts[1]))
                return
            }
            clim.Send(fmt.Sprintf("Online members of the cluster #%d:\n", idx))
            ridx := 0
            sort.Slice(rtp_cluster.active, func(i, j int) bool {return rtp_cluster.active[i].name < rtp_cluster.active[j].name})
            for _, rtpp := range rtp_cluster.active {
                if ridx > 0 {
                    clim.Send("\n")
                }
                clim.Send(fmt.Sprintf("    RTPproxy: #%d\n", ridx))
                clim.Send(fmt.Sprintf("        name = %s\n", rtpp.name))
                clim.Send(fmt.Sprintf("        address = %s\n", string(rtpp.Address().String())))
                if rtpp.wan_address != "" {
                    clim.Send(fmt.Sprintf("        wan_address = %s\n", rtpp.wan_address))
                }
                if rtpp.lan_address != "" {
                    clim.Send(fmt.Sprintf("        lan_address = %s\n", rtpp.lan_address))
                }
                if rtpp.cmd_out_address != nil {
                    clim.Send(fmt.Sprintf("        cmd_out_address = %s\n", rtpp.cmd_out_address.String()))
                }
                clim.Send(fmt.Sprintf("        weight = %d\n", rtpp.weight))
                clim.Send(fmt.Sprintf("        capacity = %d\n", rtpp.capacity))
                clim.Send("        state = ")
                if rtpp.IsOnline() {
                    clim.Send("online\n")
                    clim.Send("        active sessions = ")
                    if rtpp.GetActiveSessions() < 0 {
                        clim.Send("UNKNOWN\n")
                    } else {
                        clim.Send(fmt.Sprintf("%d\n", rtpp.GetActiveSessions()))
                    }
                    clim.Send(fmt.Sprintf("        capacity utilization = %f%%\n", rtpp.get_caputil() * 100))
                    clim.Send(fmt.Sprintf("        average rtpc delay = %f sec\n", rtpp.GetRtpcDelay()))
                } else {
                    clim.Send("offline\n")
                }
                clim.Send(fmt.Sprintf("        status = %s\n", rtpp.status.String()))
                ridx += 1
            }
            clim.Send(fmt.Sprintf("\nOffline members of the cluster #%d:\n", idx))
            ridx = 0
            for _, rtpp := range rtp_cluster.pending {
                if ridx > 0 {
                    clim.Send("\n")
                }
                clim.Send(fmt.Sprintf("    RTPproxy: #%d\n", ridx))
                clim.Send(fmt.Sprintf("        name = %s\n", rtpp.name))
                clim.Send(fmt.Sprintf("        address = %s\n", rtpp.Address().String()))
                if rtpp.wan_address != "" {
                    clim.Send(fmt.Sprintf("        wan_address = %s\n", rtpp.wan_address))
                }
                if rtpp.lan_address != "" {
                    clim.Send(fmt.Sprintf("        lan_address = %s\n", rtpp.lan_address))
                }
                if rtpp.cmd_out_address != nil {
                    clim.Send(fmt.Sprintf("        cmd_out_address = %s\n", rtpp.cmd_out_address.String()))
                }
                clim.Send(fmt.Sprintf("        weight = %d\n", rtpp.weight))
                clim.Send(fmt.Sprintf("        capacity = %d\n", rtpp.capacity))
                clim.Send("        state = ")

                if rtpp.IsOnline() {
                    clim.Send("online\n")
                } else {
                    clim.Send("offline\n")
                }
                ridx += 1
            }
            if ridx == 0 {
                clim.Send("\n")
            }
        }
        clim.Send("OK\n")
        return
    case "modify":
        parts := sippy_utils.FieldsN(cmd, 5)
        rtp_cluster, _ := self.cluster_by_name(parts[1])
        if rtp_cluster == nil {
            clim.Send(fmt.Sprintf("ERROR: %s: cluster not found\n", parts[1]))
            return
        }
        switch parts[2] {
        case "add":
            kvs := strings.Split(parts[3], ",")
            rtpp_config := make(map[string]string)
            for _, v := range kvs {
                kvsparts := strings.Split(v, "=")
                rtpp_config[kvsparts[0]] = kvsparts[1]
            }
            rtpp, _ := rtp_cluster.rtpp_by_name(rtpp_config["name"])
            if rtpp != nil {
                clim.Send(fmt.Sprintf("ERROR: %s: RTPproxy already exists\n", rtpp_config["name"]))
                return
            }
            switch rtpp_config["protocol"] {
            case "unix":
            case "udp":
            case "udp6":
            default:
                self.logger.Debugf("Unsupported RTPproxy protocol: %s", rtpp_config["protocol"])
                return
            }
            address := rtpp_config["address"]
            switch rtpp_config["protocol"] {
            case "udp": fallthrough
            case "udp6":
                if _, _, err := net.SplitHostPort(address); err != nil {
                    address += ":22222"
                }
            }
            var bind_address *sippy_net.HostPort
            if ip, port, err := net.SplitHostPort(rtpp_config["cmd_out_address"]); err == nil {
                bind_address = sippy_net.NewHostPort(ip, port)
            }
            rtpp = NewRtp_cluster_member(rtpp_config["name"], self.global_config, rtpp_config["protocol"], address, bind_address)
            if _, ok := rtpp_config["wan_address"]; ok {
                rtpp.wan_address = rtpp_config["wan_address"]
            }
            if _, ok := rtpp_config["lan_address"]; ok {
                rtpp.lan_address = rtpp_config["lan_address"]
            }
            var err error
            rtpp.weight, err = strconv.Atoi(rtpp_config["weight"])
            if err != nil {
                self.logger.Debugf("Wrong weight: %s", rtpp_config["weight"])
                return
            }
            rtpp.capacity, err = strconv.Atoi(rtpp_config["capacity"])
            if err != nil {
                self.logger.Debugf("Wrong capacity: %s", rtpp_config["capacity"])
                return
            }
            rtpp.status = RTPCMStatusFromString(rtpp_config["status"])
            rtp_cluster.add_member(rtpp)
            clim.Send("OK\n")
            return
        case "remove": fallthrough
        case "delete": fallthrough
        case "pause": fallthrough
        case "resume":
            rtpp, _ := rtp_cluster.rtpp_by_name(parts[3])
            if rtpp == nil {
                clim.Send(fmt.Sprintf("ERROR: %s: RTPproxy not found\n", parts[3]))
                return
            }
            if parts[2] == "remove" || parts[2] == "delete" {
                rtp_cluster.bring_down(rtpp)
            } else if parts[2] == "pause" {
                rtpp.status = RTPCM_STATUS_SUSPENDED
            } else if parts[2] == "resume" {
                rtpp.status = RTPCM_STATUS_ACTIVE
            }
            clim.Send("OK\n")
            return
        default:
            clim.Send(fmt.Sprintf("ERROR: unsupported action: \"%s\"\n", parts[2]))
            return
        }
    case "h":fallthrough
    case "help":
        clim.Send("Supported commands:\n" +
            "\tls [CLUSTER_NAME]\n" +
            "\tmodify CLUSTER_NAME [add|remove|delete|pause|resume] ARGS\n" +
            "\treload\n" +
            "\tquit\n")
        return
    case "q": fallthrough
    case "quit": fallthrough
    case "exit":
        clim.Close()
        return
    case "reload":
        bytes, err := ioutil.ReadFile(self.global_config.conffile)
        if err != nil {
            self.logger.Error(err.Error())
        }
        config := Read_cluster_config(self.logger, string(bytes), false)
        new_rtpps_count := 0
        new_rtp_clusters := []*rtp_cluster{}
        for _, c := range config {
            var err error
            rtp_cluster, _ := self.cluster_by_name(c.name)
            if rtp_cluster == nil {
                rtp_cluster, err = NewRtp_cluster(self.global_config, c.name, c.protocol, c.address, c.dnconfig)
                if err != nil {
                    clim.Send("Error creating Rtp_cluster instance: " + err.Error())
                    return
                }
            } else {
                rtp_cluster.update_dnrelay(c.dnconfig)
            }
            rtp_cluster.capacity_limit_soft = c.capacity_limit_soft
            new_rtpps := make([]*rtp_cluster_member, 0)
            for _, rtpp_config := range c.RtpProxies {
                switch rtpp_config.protocol {
                case "unix":
                case "udp":
                case "udp6":
                default:
                    self.logger.Debug("Unsupported RTPproxy protocol: " + rtpp_config.protocol)
                    return
                }
                address := rtpp_config.address
                var addr net.Addr
                if rtpp_config.protocol == "udp" || rtpp_config.protocol == "udp6" {
                    if _, _, err := net.SplitHostPort(address); err != nil {
                        address += ":22222"
                    }
                    addr, _ = net.ResolveUDPAddr(rtpp_config.protocol, address)
                } else {
                    addr, _ = net.ResolveUnixAddr("unix", address)
                }
                var bind_address *sippy_net.HostPort
                if ip, port, err := net.SplitHostPort(rtpp_config.cmd_out_address); err == nil {
                    bind_address = sippy_net.NewHostPort(ip, port)
                }
                rtpp, _ := rtp_cluster.rtpp_by_name(rtpp_config.name)
                if rtpp == nil {
                    rtpp = NewRtp_cluster_member(rtpp_config.name, self.global_config, rtpp_config.protocol, address, bind_address)
                    rtpp.weight = rtpp_config.weight
                    rtpp.capacity = rtpp_config.capacity
                    rtpp.wan_address = rtpp_config.wan_address
                    rtpp.lan_address = rtpp_config.lan_address
                    rtpp.status = RTPCMStatusFromString(rtpp_config.status)
                    rtp_cluster.add_member(rtpp)
                } else {
                    rtpp.cmd_out_address = bind_address
                    rtpp.Reconnect(addr, bind_address)
                    rtpp.weight = rtpp_config.weight
                    rtpp.capacity = rtpp_config.capacity
                    rtpp.wan_address = rtpp_config.wan_address
                    rtpp.lan_address = rtpp_config.lan_address
                    rtpp.status = RTPCMStatusFromString(rtpp_config.status)
                }
                new_rtpps = append(new_rtpps, rtpp)
            }
            new_rtpps_count += len(new_rtpps)
            for _, rtpp := range rtp_cluster.all_members() {
                found := false
                for _, rtpp2 := range new_rtpps {
                    if rtpp == rtpp2 {
                        found = true
                        break
                    }
                }
                if ! found {
                    rtp_cluster.bring_down(rtpp)
                }
            }
            new_rtp_clusters = append(new_rtp_clusters, rtp_cluster)
        }
        for _, rtp_cluster := range self.rtp_clusters {
            found := false
            for _, rtp_cluster2 := range(new_rtp_clusters) {
                if rtp_cluster == rtp_cluster2 {
                    found = true
                    break
                }
            }
            if ! found {
                rtp_cluster.Shutdown()
            }
        }
        self.rtp_clusters = new_rtp_clusters
        clim.Send(fmt.Sprintf("Loaded %d clusters and %d RTP proxies\n", len(self.rtp_clusters), new_rtpps_count))
        clim.Send("OK\n")
        return
    default:
        clim.Send("ERROR: unknown command\n")
    }
    return
}

func (self *Rtp_cluster_cli) cluster_by_name(name string) (*rtp_cluster, int) {
    for idx, rtp_cluster := range self.rtp_clusters {
        if rtp_cluster.name == name {
            return rtp_cluster, idx
        }
    }
    return nil, -1
}

func (self *Rtp_cluster_cli) append_cluster(c *rtp_cluster) {
    self.rtp_clusters = append(self.rtp_clusters, c)
}

func (self *Rtp_cluster_cli) clusters() []*rtp_cluster {
    return self.rtp_clusters
}
