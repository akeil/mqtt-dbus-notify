MAIN    = akeil.net/mqtt-dbus-notify

build:
	go install $(MAIN)

deps:
	go get github.com/godbus/dbus
	go get github.com/eclipse/paho.mqtt.golang
