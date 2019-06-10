GOPATH?=/usr/local/share/go
LOCALBASE?=/usr/local

all: rtp_cluster

rtp_cluster: *.go
	GOPATH=$(GOPATH) go build -o rtp_cluster

clean:
	-rm rtp_cluster

test:
	GOPATH=$(GOPATH) go test

install: rtp_cluster
	install rtp_cluster $(LOCALBASE)/bin/rtp_cluster
