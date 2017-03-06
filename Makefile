MAIN    = akeil.net/mqtt-dbus-notify
BINDIR  = ./bin

build:
	mkdir -p $(BINDIR)
	go build -o $(BINDIR)/mqtt-dbus-notify $(MAIN)

install:
	go install $(MAIN)

fmt:
	gofmt -w *.go

deps:
	go get github.com/godbus/dbus
	go get github.com/eclipse/paho.mqtt.golang
