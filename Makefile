#GOPATH  = $(CURDIR)
MAIN    = akeil.net/mqtt-dbus-notify

build:
	go install $(MAIN)

deps:
	go get anonscm.debian.org/git/pkg-go/packages/golang-dbus.git
	go get github.com/eclipse/paho.mqtt.golang

gopath:
	export GOPATH=$(GOPATH)
