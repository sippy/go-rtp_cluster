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
    "flag"
    "io/ioutil"
    "net"
    "os"
    "os/signal"
    "os/user"
    "runtime"
    "strconv"
    "strings"
    "time"

    "github.com/sippy/go-b2bua/sippy/log"
    "github.com/sippy/go-b2bua/sippy/net"
    "github.com/sippy/go-b2bua/sippy/utils"
)

type fakecli struct {
    rtp_clusters    []*rtp_cluster
}

func Newfakecli() *fakecli {
    return &fakecli {
        rtp_clusters : []*rtp_cluster{},
    }
}

func (self *fakecli) append_cluster(c *rtp_cluster) {
    self.rtp_clusters = append(self.rtp_clusters, c)
}

func (self *fakecli) clusters() []*rtp_cluster {
    return self.rtp_clusters
}

func usage() {
    println("usage: rtp_cluster [-fd] [-P pidfile] [-c conffile] [-L logfile] [-s cmd_socket] [-o uname:gname]")
    os.Exit(1)
}

func main() {
    runtime.GOMAXPROCS(runtime.NumCPU())

    var logfile string

    var foreground bool
    var dry_run bool
    //var debug_threads bool
    var pidfile string
    var csockfile string
    var usr_grp_owner, conffile string

    flag.BoolVar(&foreground,"f" ,false, "Run in foreground")
    flag.StringVar(&pidfile, "P", "/var/run/rtp_cluster.pid", "PID file")
    flag.StringVar(&conffile, "c", "/usr/local/etc/rtp_cluster.xml", "xml rtp cluster config file")
    flag.StringVar(&logfile, "L", "/var/log/rtp_cluster.log", "Log file")
    flag.StringVar(&csockfile , "s", "/var/run/rtp_cluster.sock", "Socket file")
    flag.StringVar(&usr_grp_owner, "o", "", "Run under which user:group")
    flag.BoolVar(&dry_run, "d", false, "Dry run")
    //flag.BoolVar(&debug_threads, "D", false, "for debugging threads")
    flag.Parse()


    if dry_run == true {
	foreground = true
    }

    error_logger := sippy_log.NewErrorLogger()
    sip_logger, err := sippy_log.NewSipLogger("rtp_cluster", logfile)
    if err != nil {
        error_logger.Error("cannot instantiate sipplogger")
        error_logger.Error(err)
    }
    global_config := NewMainConfig(error_logger, sip_logger)
    if usr_grp_owner != "" {
        usr_grp := strings.SplitN(usr_grp_owner, ":", 2)
	usr, err := user.Lookup(usr_grp[0])
	if err != nil {
            println("User " + usr_grp[0] + " not found")
            return
	}
        global_config.sock_uid, _ = strconv.Atoi(usr.Uid)
        if len(usr_grp) > 1 {
            grp, err := user.LookupGroup(usr_grp[1])
            if err != nil {
                println("Group " + usr_grp[1] + " not found")
                return
            }
            global_config.sock_gid, _ = strconv.Atoi(grp.Gid)
        }
    }
    global_config.conffile = conffile
    global_config.SetSipAddress(global_config.GetMyAddress())

    error_logger.Debug(" o reading config " + global_config.conffile)

    bytes, err := ioutil.ReadFile(global_config.conffile)
    if err != nil {
        error_logger.Error(err.Error())
    }
    config := Read_cluster_config(error_logger, string(bytes), false)

    if ! foreground {
        sippy_utils.AddLogReopenFunc(func() {sip_logger.Reopen() })
        sippy_utils.Daemonize(logfile, global_config.sock_uid, global_config.sock_gid, error_logger)
    }
    f, err := os.Create(pidfile)
    if err != nil {
        error_logger.Error(err.Error())
        return
    }
    if f.WriteString( strconv.Itoa(os.Getpid()) ); err != nil {
        error_logger.Error(err.Error())
        return
    }
    f.Close()
    error_logger.Debug(" o initializing CLI...")

    var cli Rtp_cluster_cli_iface
    if ! dry_run {
        cli, err = NewRtp_cluster_cli(global_config, csockfile)
        if err != nil {
            error_logger.Error("Cannot create Rtp_cluster_cli: " + err.Error())
            return
        }
    } else {
        cli = Newfakecli()
    }
    for _, c := range config {
        //print 'Rtp_cluster', global_config, c['name'], c['address']
        error_logger.Debugf(" o initializing cluster \"%s\" at <%s>", c.name, c.address)
        rtp_cluster, err := NewRtp_cluster(global_config, c.name, c.protocol, c.address, c.dnconfig)
        if err != nil {
            error_logger.Error("Cannot create Rtp_cluster: " + err.Error())
            return
        }
        rtp_cluster.capacity_limit_soft = c.capacity_limit_soft
        for _, rtpp_config := range c.RtpProxies {
            error_logger.Debugf("  - adding RTPproxy member %s at <%s>", rtpp_config.name, rtpp_config.address)
            //Rtp_cluster_member('rtpproxy1', global_config, ('127.0.0.1', 22222))
            switch rtpp_config.protocol {
                case "unix":
                case "udp":
                case "udp6":
                default:
                error_logger.Errorf("Unsupported RTPproxy protocol: \"%s\"", rtpp_config.protocol)
                return
            }
            address := rtpp_config.address
            if rtpp_config.protocol == "udp" || rtpp_config.protocol == "udp6" {
                if _, _, err := net.SplitHostPort(rtpp_config.address); err != nil {
                    address += ":22222"
                }
            } else {
            }
            var bind_address *sippy_net.HostPort
            if ip, port, err := net.SplitHostPort(rtpp_config.cmd_out_address); err == nil {
                bind_address = sippy_net.NewHostPort(ip, port)
            }
            rtpp := NewRtp_cluster_member(rtpp_config.name, global_config, rtpp_config.protocol, address, bind_address)
            rtpp.weight = rtpp_config.weight
            rtpp.capacity = rtpp_config.capacity
            rtpp.status = RTPCMStatusFromString(rtpp_config.status)
            rtpp.wan_address = rtpp_config.wan_address
            rtpp.lan_address = rtpp_config.lan_address
            rtp_cluster.add_member(rtpp)
        }
        cli.append_cluster(rtp_cluster)
    }
    //rtp_cluster = Rtp_cluster(global_config, "supercluster", dry_run = dry_run)
    if dry_run {
        error_logger.Debug("Configuration check is complete, no errors found")
        for _, rtp_cluster := range cli.clusters() {
            rtp_cluster.Shutdown()
        }
        //sip_logger.Shutdown()
        // Give worker threads some time to cease&desist
        time.Sleep(100 * time.Millisecond)
        os.Exit(0)
    }
    error_logger.Debug("Initialization complete, have a good flight.")
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt)
    <-c
}
