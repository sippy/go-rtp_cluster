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
    "io/ioutil"
    "testing"

    "github.com/sippy/go-b2bua/sippy/log"
)

func TestRead_cluster_config(t *testing.T) {
    logger := sippy_log.NewErrorLogger()

    bytes, err := ioutil.ReadFile("rtp_cluster.xml")
    if err != nil {
	t.Error(err.Error())
    }

    clusters := Read_cluster_config(logger, string(bytes), false)
    if len(clusters) == 0 {
        t.Error(err.Error())
    }

    logger.Debugf("clusters: %#v", clusters)
    logger.Debug("Read func OK!\n")
}

func TestGen_cluster_config(t *testing.T) {
    logger := sippy_log.NewErrorLogger()

    bytes, err := ioutil.ReadFile("rtp_cluster.xml")
    if err != nil {
	t.Error(err.Error())
    }

    clusters := Read_cluster_config(logger, string(bytes), false)

    xml := Gen_cluster_config(clusters)
    if xml == "" {
        t.Error("The xml was not generated properly!")
    }

    logger.Debug("Gen func OK!\n")

}

func TestMain(t *testing.T) {
    logger := sippy_log.NewErrorLogger()

    logger.Debug("Reading config...")
    bytes, err := ioutil.ReadFile("rtp_cluster.xml")
    if err != nil {
	t.Error(err.Error())
    }
    clusters := Read_cluster_config(logger, string(bytes), false)

    logger.Debug("Generating config...")
    xml := Gen_cluster_config(clusters)

    if xml == "" {
        t.Error("The xml was not generated properly!")
    }

    logger.Debug("Reading generated config...")
    clusters = Read_cluster_config(logger, xml, false)

    if len(clusters) == 0 {
        t.Error("The clusters are not loaded properly!")
    }

    logger.Debug("The main func: OK!\n")

}

func TestNewRtp_cluster_member(t *testing.T) {
    logfile := "/var/log/test_rtp_cluster.log"
    csockfile := "/tmp/rtpc.sock"
    logger := sippy_log.NewErrorLogger()
    sip_logger, err := sippy_log.NewSipLogger("test_rtp_cluster", logfile)
    if err != nil {
        t.Error("cann't instantiate sip_logger")
    }
    global_config := NewMainConfig(logger, sip_logger)
    global_config.conffile = "/usr/local/etc/basic_network.xml"
    global_config.SetSipAddress(global_config.GetMyAddress())

    logger.Debugf("   o reading config %s\n", global_config.conffile)

    rtpp := NewRtp_cluster_member("RTPPROXY0", global_config, "unix", csockfile, nil)
    rtpp.Start()
    resch := make(chan bool, 1)
    rtpp.SendCommand("ls", func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch

    rtpp.SendCommand("reload", func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch

    acmd := "modify basic_network pause RTPPROXY2"

    rtpp.SendCommand(acmd, func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch

    rtpp.SendCommand("ls basic_network", func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch

    acmd = "modify basic_network delete RTPPROXY0"

    rtpp.SendCommand(acmd, func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch

    rtpp.SendCommand("ls basic_network", func(cmd string) {logger.Debugf("the result is: %s", cmd); resch <- true})

    <-resch
    logger.Debug("NewRtp_cluster_member OK!\n")
}
