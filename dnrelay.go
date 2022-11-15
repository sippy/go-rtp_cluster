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
    "net"
    "strings"
    "sync"
    "syscall"
    "sippy/cli"
    "sippy/log"
    "sippy/net"
)

const _MAX_TRIES = 10

type DNWorker struct {
    spath           string
    s               *net.UnixConn
    wi              chan string         // work item
    logger          sippy_log.ErrorLogger
    shutdown_chan   chan bool
}

func newDNWorker(spath string, logger sippy_log.ErrorLogger) (*DNWorker, error) {
    spath = strings.TrimPrefix(spath, "unix:")
    self := &DNWorker{
        spath		: spath,	            // the path of socket file!
        wi		: make(chan string, 1000),  // it is a list of works
        logger          : logger,
        shutdown_chan	: make(chan bool, 1),
    }
    go self.run()
    return self, nil
}

func (self *DNWorker) Connect() error {

    raddr, err := net.ResolveUnixAddr("unix", self.spath)
    if err != nil {
        self.logger.Errorf("Error resolving %s: %s", self.spath, err.Error())
        return err
    }

    self.s, err = net.DialUnix("unix", nil, raddr)
    if err != nil {
        self.logger.Errorf("Error connecting to %s: %s", self.spath, err.Error())
        return err
    }
    // start a drain loop instead of using poll() or select()
    go func() {
        buf := make([]byte, 1024)
        for {
            if _, err := self.s.Read(buf); err != nil {
                return
            }
        }
    }()

    return nil
}

func (self *DNWorker) deliver_dnotify(dnstring string) error {
     if self.s == nil {
        if err := self.Connect(); err != nil {
            return err
        }
     }

    if !strings.HasSuffix(dnstring, "\n") {
        dnstring = dnstring + "\n"
    }

    tries := 0
    for {
         if tries > _MAX_TRIES {
            return fmt.Errorf("Cannot reconnect: %s", self.spath)
         }
         tries++

         if _, err := self.s.Write([]byte(dnstring)); err != nil {
             if err == syscall.EINTR {
                continue
             } else if (err == syscall.EPIPE) || (err == syscall.ENOTCONN) || (err == syscall.ECONNRESET) {
                self.s = nil
                continue
            } else {
		return err
	    }
        }

        return nil
    }
}

func (self *DNWorker) run() {
    for {
        select {
        case wi := <-self.wi:
            err := self.deliver_dnotify(wi)
            if err != nil {
                self.logger.Error("Cannot deliver notification " + wi + " to the " + self.spath + ": " + err.Error())
            } else {
                self.logger.Debug("Notification " + wi + "delivered to the " + self.spath)
            }
        case <-self.shutdown_chan:
            return
        }
    }
}

func (self *DNWorker) Send_denotify(dnstring string) {
    self.wi<- dnstring
}

type DNRelay struct {
    clim            *sippy_cli.CLIConnectionManager
    workers         map[string]*DNWorker
    lock            sync.Mutex
    dest_sprefix    string
    in_address      *sippy_net.HostPort
    logger          sippy_log.ErrorLogger
}

func NewDNRelay(dnconfig *DisconnectNotify, logger sippy_log.ErrorLogger) (*DNRelay, error) {
    ip, port, err := net.SplitHostPort(dnconfig.In_address)
    if err != nil {
        return nil, err
    }
    in_address := sippy_net.NewHostPort(ip, port)
    self := &DNRelay {
        workers         : make(map[string]*DNWorker),
        dest_sprefix    : dnconfig.Dest_sprefix,
        in_address	: in_address,
        logger          : logger,
    }
    self.clim, err = sippy_cli.NewCLIConnectionManagerTcp(self.Recv_dnotify, dnconfig.In_address, logger)
    if err != nil {
        return nil, err
    }
    self.clim.Start()
    return self, nil
}

func (self *DNRelay) Recv_dnotify(clim sippy_cli.CLIManagerIface, dnstring string) {
    raddr, _, _ := net.SplitHostPort(clim.RemoteAddr().String())
    if raddr != "" {
        self.logger.Debug("Disconnect notification from " + raddr + " received on " + self.in_address.String() + " :"+ dnstring )
    } else {
        self.logger.Debug("Disconnect notification received on " + self.in_address.String() + ": " + dnstring )
    }
    dnparts := strings.SplitN(dnstring, " ", 2)
    spath := self.dest_sprefix + strings.Trim(dnparts[0], "")
    dnstring = strings.Trim(dnparts[1], "")
    self.lock.Lock()
    dnw, ok := self.workers[spath]
    if ! ok {
        var err error
        dnw, err = newDNWorker(spath, self.logger)
        if err != nil {
            self.logger.Errorf("Cannot start a DNWorker: %s", err.Error())
        } else {
            self.workers[spath] = dnw
        }
    }
    self.lock.Unlock()
    self.logger.Debug("forwarding notification to " + spath + ": " + dnstring)
    dnw.Send_denotify(dnstring)
    return
}

func (self *DNRelay) Shutdown() {
    for _, rworker := range self.workers {
        rworker.shutdown_chan <- true
    }
    self.clim.Shutdown()
    self.workers = nil
}

func (self *DNRelay) Cmpconfig(dnconfig *DisconnectNotify) bool{
    if dnconfig.Dest_sprefix != self.dest_sprefix {
        return false
    }
    if dnconfig.In_address != self.in_address.String() {
        return false
    }
    return true
}

func (self *DNRelay) Get_allow_list() []string {
    return self.clim.GetAcceptList()
}

func (self *DNRelay) Set_allow_list(accept_list []string){
    self.clim.SetAcceptList(accept_list)
}

func (self *DNRelay) AppendAllowFrom(addr net.Addr) {
    ip, _, err := net.SplitHostPort(addr.String())
    if err == nil {
        self.clim.AcceptListAppend(ip)
    }
}

func (self *DNRelay) disallow_from(address string) {
        self.clim.AcceptListRemove(address)
    }

/*
func main() {
    selfs, err := net.Dial("unix", "/tmp/aSocket.sock")
    if err != nil {
	log.Println("dialing error:"+ err.Error())
	time.Sleep(100 * time.Millisecond)
	return
    }

    log.Printf("%+v", selfs)
        n, err := syscall.Select(self.s, nil, nil, nil, nil)
        if err != nil {
            log.Println("There is an issue in select!")
            return err
        }
        log.Println(n)

    fmt.Println("You are in the zero step!")

    dnconfig := newDNConfig()

    aworker := newDNWorker("anewDNWorker", sippy_log.newErrorLogger())

    log.Printf("this is aWorker: %+v", aworker)

    log.Printf("%+v", dnconfig)

}
*/
