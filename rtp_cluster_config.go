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
    "encoding/xml"
    "fmt"
    "strconv"
    "strings"

    "sippy/log"
)

type DisconnectNotify struct {
    Section_name string
    In_address string // TODO, It should be changed!
    Dest_sprefix string
}

func NewDisconnectNotify(section_name string) *DisconnectNotify {
    return &DisconnectNotify {
        Section_name    : section_name,
	In_address      : "",
	Dest_sprefix    : "",
    }
}

func (self *DisconnectNotify) Set_in_address(in_address_str string) {
    self.In_address = in_address_str
}

func (self *DisconnectNotify) String(ident string, idlevel int) string {

    return fmt.Sprintf("%s<%s>\n%s<inbound_address>%s</inbound_address>\n" +
        "%s<dest_socket_prefix>%s</dest_socket_prefix>\n%s</%s> ",
        strings.Repeat(ident, idlevel), self.Section_name,
        strings.Repeat(ident, idlevel + 1), self.In_address,
        strings.Repeat(ident, idlevel + 1), self.Dest_sprefix,
        strings.Repeat(ident, idlevel), self.Section_name)
}

type RtpCluster struct {
    name string
    protocol string
    address string
    RtpProxies []*RtpProxy
    dnconfig *DisconnectNotify
    capacity_limit_soft bool
}

func NewRtpCluster() *RtpCluster{
    self := RtpCluster {
        protocol            : "UDP",
        address             : "192.168.56.2:44",
        capacity_limit_soft : false,
        RtpProxies          : make([]*RtpProxy, 0),
    }

    return &self
}

type RtpProxy struct {
    name        string
    protocol    string
    address     string
    weight      int
    capacity    int
    status      string
    wan_address string
    lan_address string
    cmd_out_address string
}

func NewRtpProxy() *RtpProxy {
    return &RtpProxy{
        protocol        : "UDP",
        address         : "127.0.0.1:22222",
        weight          : 100,
        capacity        : 4000,
        status          : "ACTIVE",
        wan_address     : "",
        lan_address     : "",
        cmd_out_address : "",
    }
}

type ValidateHandler struct {
    config      []*RtpCluster
    rtp_cluster *RtpCluster
    rtpproxy    *RtpProxy
    element     string
    warnings    []string
    errors      []string
    dnconfig    *DisconnectNotify
    ctx         []string
}

func NewValidateHandler() *ValidateHandler{
    self := &ValidateHandler{
        element     : "",
        ctx         : make([]string, 0),
        warnings    : make([]string, 0),
        errors      : make([]string, 0),
        config      : make([]*RtpCluster, 0),
    }

    return self
}

func (self *ValidateHandler) StartElement(elm xml.StartElement) {
    self.element = elm.Name.Local

    if self.element == "rtp_cluster_config" {
	self.ctx = append(self.ctx, "rtp_cluster_config")
    } else if self.element == "rtp_cluster" {
        self.rtp_cluster = NewRtpCluster()
        self.config = append(self.config, self.rtp_cluster)
        self.ctx = append(self.ctx, "rtp_cluster")
    } else if self.element == "rtpproxy" {
        self.rtpproxy = NewRtpProxy()
        self.rtp_cluster.RtpProxies = append(self.rtp_cluster.RtpProxies, self.rtpproxy)
        self.ctx = append(self.ctx, "rtpproxy")
    } else if self.element == "disconnect_notify" {
        self.dnconfig = NewDisconnectNotify(self.element)
        self.rtp_cluster.dnconfig = self.dnconfig
        self.ctx = append(self.ctx, "dnconfig")
    } else if self.element == "capacity_limit" && self.ctx[len(self.ctx)-1] == "rtp_cluster" {
        for _, v := range elm.Attr {
            if v.Name.Local == "type" {
                if v.Value == "soft" {
                    self.rtp_cluster.capacity_limit_soft = true
                } else {
                    self.rtp_cluster.capacity_limit_soft = false
                }
                break
            }
        }

    }
}

func (self *ValidateHandler) EndElement(elm xml.EndElement) {
    name := elm.Name.Local
    if name == "rtp_cluster" {
        self.rtp_cluster = nil
        self.ctx = self.ctx[:len(self.ctx)-1]
    } else if name == "rtpproxy" {
        self.rtpproxy = nil
        self.ctx = self.ctx[:len(self.ctx)-1]
    } else if name == "disconnect_notify" {
        self.dnconfig = nil
        self.ctx = self.ctx[:len(self.ctx)-1]
    } else if name == self.element {
        self.element = ""
    }

}

func (self *ValidateHandler) Characters(elm xml.CharData) error{
    content := string([]byte(elm))
    if self.ctx[len(self.ctx)-1] == "rtp_cluster" {
        if self.element == "name" {
            for _, c := range self.config {
                if c.name == content {
                    return fmt.Errorf("rtp_cluster name should be unique %s", content)
                }
            }
            self.rtp_cluster.name = content
        } else if self.element == "protocol" {
            self.rtp_cluster.protocol = strings.ToLower(content)
            if self.rtp_cluster.protocol != "udp" &&self.rtp_cluster.protocol != "udp6" &&self.rtp_cluster.protocol != "unix" {
                return fmt.Errorf("rtp_cluster protocol should be one of udp, udp6, or unix")
            }
        } else if self.element == "address" {
            self.rtp_cluster.address = content
        }
    } else if self.ctx[len(self.ctx)-1] == "rtpproxy" {
        if self.element == "name" {
            for _, c := range self.rtp_cluster.RtpProxies {
                if c.name == content {
                    return fmt.Errorf("rtpproxy name should be unique within rtp_cluster: %s", content)
                }
            }
            self.rtpproxy.name = content
        } else if self.element == "protocol" {
            self.rtpproxy.protocol = strings.ToLower(content)
            if self.rtpproxy.protocol != "udp" && self.rtpproxy.protocol != "udp6" && self.rtpproxy.protocol != "unix" {
                return fmt.Errorf("rtpproy protocol should be one of udp, udp6, or unix")
            }
        } else if self.element == "address" {
            self.rtpproxy.address = content
        } else if self.element == "wan_address" {
            self.rtpproxy.wan_address = content
        } else if self.element == "lan_address" {
            self.rtpproxy.lan_address = content
        } else if self.element == "cmd_out_address" {
            self.rtpproxy.cmd_out_address = content
        } else if self.element == "weight" {
            if weight, err := strconv.Atoi(content); err == nil {
                self.rtpproxy.weight = weight
            } else {
                return fmt.Errorf("wrong rtpproxy weight value, an integer is expected: %s", content)
            }
            if self.rtpproxy.weight <= 0 {
                return fmt.Errorf("rtpproxy weight should > 0: %s ", content)
            }
        } else if self.element == "capacity" {
            if capacity, err := strconv.Atoi(content); err == nil {
                self.rtpproxy.capacity = capacity
            } else {
                return fmt.Errorf("wrong rtpproxy capacity value, an integer is expected: %s", content)
            }
            if self.rtpproxy.capacity <= 0 {
                return fmt.Errorf("wrong rtpproxy capacity value, an integer is expected: %s", content)
            }
        } else if self.element == "status" {
            self.rtpproxy.status = strings.ToLower(content)
            if self.rtpproxy.status != "suspended" && self.rtpproxy.status != "active" {
                return fmt.Errorf("rtpproxy status should be either 'suspended' or 'active'")
            }
        }
    } else if self.ctx[len(self.ctx)-1] == "dnconfig" {
        if self.element == "inbound_address" {
            self.dnconfig.Set_in_address(content)
        } else if self.element == "dest_socket_prefix" {
            self.dnconfig.Dest_sprefix = content
        }
    }
    return nil
}

func (self *ValidateHandler) Error(err string) {
    self.errors = append(self.errors, err)
}

func Read_cluster_config(logger sippy_log.ErrorLogger, config string, debug bool) []*RtpCluster {
    configReader := strings.NewReader(config)

    parsed_config := NewValidateHandler()
    parser := xml.NewDecoder(configReader)
    for {
	token, err := parser.Token()
	if err != nil {
	    break
	}
	switch tkn := token.(type) {
	case xml.StartElement:
            parsed_config.StartElement(xml.StartElement(tkn))
	case xml.EndElement:
            parsed_config.EndElement(xml.EndElement(tkn))
	case xml.CharData:
            parsed_config.Characters(tkn)
	case xml.SyntaxError:
            synErr := xml.SyntaxError(tkn)
            parsed_config.Error(synErr.Error())
	case xml.TagPathError:
            tagErr := xml.TagPathError(tkn)
            parsed_config.Error(tagErr.Error())
	//default:
        //    logger.Debug("unknown")
	}
    }

    if len(parsed_config.errors) > 1 {
        for _, v := range parsed_config.errors {
            logger.Debug(v)
        }
    }

    if debug {
        aDebugProxy := fmt.Sprintf("Parsed: %#v", parsed_config.config[0].RtpProxies[0])
        logger.Debug(aDebugProxy)
    }

    return parsed_config.config
}

func Gen_cluster_config(clusters []*RtpCluster) string{
    xml := "<rtp_cluster_config>\n\n"

    for _, cluster := range clusters {
        xml += "  <rtp_cluster>\n"
        xml += "    <name>" + strings.TrimSpace(cluster.name) + "</name>\n"
        xml += "    <protocol>" + strings.TrimSpace(cluster.protocol) + "</protocol>\n"

        // TODO The address should be corrected properly!
        if cluster.protocol == "udp" || cluster.protocol == "udp6" {
            xml += "    <address>" + strings.TrimSpace(cluster.address) + "</address>\n\n"
        } else {
            xml += "    <address>" + strings.TrimSpace(cluster.address) + "</address>\n\n"
        }

        dnconfig := cluster.dnconfig
        if dnconfig != nil {
            xml += dnconfig.String("  ", 2)
        }

        cl_type := cluster.capacity_limit_soft
        xml += "    <capacity_limit_type=" + strconv.FormatBool(cl_type) + ">\n\n"

        for _, proxy := range cluster.RtpProxies {
            xml += "    <rtpproxy>\n"
            xml += "      <name>" + strings.TrimSpace(proxy.name) + "</name>\n"
            xml += "      <protocol>" + strings.TrimSpace(proxy.protocol) + "</protocol>\n"
            xml += "      <address>" + strings.TrimSpace(proxy.address) + "</address>\n"
            xml += "      <weight>" + strconv.Itoa(proxy.weight) + "</weight>\n"
            xml += "      <capacity>" + strconv.Itoa(proxy.capacity) + "</capacity>\n"
            xml += "      <status>" + strings.TrimSpace(proxy.status) + "</status>\n"
            if proxy.wan_address != "" {
                xml += "      <wan_address>" + strings.TrimSpace(proxy.wan_address) + "</wan_address>\n"
            }
            if proxy.lan_address != "" {
                xml += "      <lan_address>" + strings.TrimSpace(proxy.lan_address) + "</lan_address>\n"
            }
            if proxy.cmd_out_address != "" {
                xml += "      <cmd_out_address>" + strings.TrimSpace(proxy.cmd_out_address) + "</cmd_out_address>\n"
            }
            xml += "    </rtpproxy>\n"
        }

        xml += "  </rtp_cluster>\n\n"
    }

    xml += "</rtp_cluster_config>\n"
    return xml
}
