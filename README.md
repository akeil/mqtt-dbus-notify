# MQTT to D-Bus Notifications
This is a small program which can subscribe to one ore more
[MQTT](http://mqtt.org/) topics and publish them as desktop notifications via
[D-Bus](https://www.freedesktop.org/wiki/Software/dbus/).
The program is intended to run in the background of a desktop session
and generate desktop notifications from MQTT messages.


## Requirements
- A running MQTT message broker (e.g. [Mosquitto](https://mosquitto.org/)).
- A Linux desktop environment which supports notifications
  and which publishes a D-Bus interface for creating notifications


## Installation
The only option right no is to install from source.
First, [Install Go](https://golang.org/doc/install).

Next, Install Go dependencies:
- [Go bindings for D-Bus](github.com/godbus/dbus)
- [Go MQTT client](https://github.com/eclipse/paho.mqtt.golang)

```
$ go get github.com/godbus/dbus
$ go get github.com/eclipse/paho.mqtt.golang
```
Next, install the mqtt-dbus-notify app:
```
$ go install github.com/akeil/mqtt-dbus-notify
```

## Configuration
The configuration file is expected at `$HOME/.config/mqtt-dbus-notify.json`.
A sample configuration looks like this:

```json
{
    "host": "localhost",
    "port": 1883,
    "username": "",
    "password": "",
    "secure": false,
    "timeout": 5,
    "icon": "dialog-information",
    "subscriptions": [
        {
            "topic": "calendar/alert",
            "icon": "appointment-soon",
            "title": "Appointment",
            "body": "{{.}}"
        },
        {
            "topic": "test/notify",
        }
    ]
}
```

Aside from the `subscriptions`, these are also the default values.
If the MQTT broker is running on the same computer on the default port (`1883`)
and without authentication, no configuration is required.

The `secure` option uses a TLS encrypted connection, usually over port `8883`.


### Subscriptions
To generate notifications, one or more *Subscriptions* need to be configured.
A subscription must at least specify one `topic `.
[Topic wildcards](https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/os/mqtt-v3.1.1-os.html#_Toc398718107)
can be used.

A subscription can also specify a custom `icon`. If none is specified,
the default icon will be used (see below).

By default, the body of the MQTT message is used as the title for the
notification. If the message consists of multiple lines, the first line is used
as the title and the remaining lines as the body.

A subscription can have a customized `title` and `body`.
These are [Go templates](https://golang.org/pkg/text/template/).
Use `{{.}}` to refer to the MQTT message payload.


### Icons
Icons can be specified using
[standard icon names](https://specifications.freedesktop.org/icon-naming-spec/icon-naming-spec-latest.html)
like "appointment-soon".
Look at the directory structure below `/usr/share/icons/` to see which icons
are available.

Alternatively, the absolute path to an image file can be used as an `icon`.
like this:
```json
{
    "topic": "my/topic",
    "icon": "/home/yourname/myicon.png"
}
```


## Running
The program needs to run within the context of a desktop session.
Otherwise, it would not be able to publish notifications.

A simple way to do this is to create a `mqtt-dbus-notify.desktop` file
and place it in `$HOME/.config/autostart`.
```ini
[Desktop Entry]
Name=MQTT-DBus-Notify
Comment=Desktop notifications from MQTT messages
NoDisplay=false
Exec=$GOHOME/bin/mqtt-dbus-notify
Type=Application
Categories=Accessoires;
```

Most (all?) Desktop Environments should support this and run the command
listed under `Exec` when you log into the DE.
