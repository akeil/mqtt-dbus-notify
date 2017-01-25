package main

import (
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "log"
    "os"
    "os/signal"
    "os/user"
    "path/filepath"
    "time"
    "strings"

    dbus "anonscm.debian.org/git/pkg-go/packages/golang-dbus.git"
    mqtt "github.com/eclipse/paho.mqtt.golang"
)


const NOTIFY_METHOD = "org.freedesktop.Notifications.Notify"
const APPNAME = "mqtt-dbus-notify"
const DESTINATION = "org.freedesktop.Notifications"
const OBJ_PATH = dbus.ObjectPath("/org/freedesktop/Notifications")


var config *Config
var dbusConn *dbus.Conn
var notifications dbus.BusObject
var mqttClient mqtt.Client


func main() {
    err := run()
    if err != nil {
        log.Fatal(err)
    }
}


func run() error {
    // setup channel to receive SIGINT (ctrl+c)
    s := make(chan os.Signal, 1)
	signal.Notify(s, os.Interrupt)

    err := loadConfig()
    if err != nil {
        return err
    }

    err = connectDBus()
    if err != nil {
        return err
    }
    defer disconnectDBus()

    err = connectMQTT()
    if err != nil {
        return err
    }
    defer disconnectMQTT()

    err = subscribe()
    if err != nil {
        return err
    }
    defer unsubscribe()

    // blocks until SIGINT
    _ = <- s
    return nil
}


// DBUS -----------------------------------------------------------------------


func connectDBus() error {
    log.Println("Connect to DBus...")
    conn, err := dbus.SessionBus()
    if err != nil {
        return err
    }

    dbusConn = conn  // global
    notifications = dbusConn.Object(DESTINATION, OBJ_PATH)  // global

    return nil
}


func disconnectDBus() {
    if dbusConn != nil {
        dbusConn.Close()
        log.Println("Disconnected from DBus")
    }
}


func notify(scfg *Subscription, title string, body string) error {
    icon := config.Icon
    log.Println(scfg)
    log.Println(scfg.Topic)
    log.Println(scfg.Icon)
    call := notifications.Call(NOTIFY_METHOD, 0, APPNAME, uint32(0), icon,
        title, body,
        []string{}, map[string]dbus.Variant{}, int32(7000))
    if call.Err != nil {
        return call.Err
    }
    return nil
}


// MQTT -----------------------------------------------------------------------


func connectMQTT() error {
    log.Println("Connect to MQTT ...")
    uri := fmt.Sprintf("tcp://%v:%v", config.Host, config.Port)
    opts := mqtt.NewClientOptions()
    opts.AddBroker(uri)

    hostname, err := os.Hostname()
    if err == nil {
        opts.SetClientID(APPNAME + "-" + hostname)
    }
    mqttClient = mqtt.NewClient(opts)  // global

    timeout := time.Duration(config.Timeout) * time.Second
    t := mqttClient.Connect()
    if !t.WaitTimeout(timeout) {
        return errors.New("MQTT Connect timed out")
    }
    return t.Error()
}


func disconnectMQTT() {
    if mqttClient != nil {
        if mqttClient.IsConnected() {
            mqttClient.Disconnect(250)  // 250 millis cleanup time
            log.Println("Disconnected from MQTT")
        }
    }
}


func subscribe() error {
    if len(config.Subscriptions) == 0 {
        log.Println("WARNING: No subscriptions configured.")
        return nil
    }

    timeout := time.Duration(config.Timeout) * time.Second
    qos := byte(0)

    for _, sub := range(config.Subscriptions) {
        if sub.Topic == "" {
            log.Println("WARNING: Ignoring subscription without topic.")
            continue
        }
        log.Printf("Subscribe to %s", sub.Topic)
        s := sub  // local var for scope
        t := mqttClient.Subscribe(sub.Topic, qos, func(client mqtt.Client, message mqtt.Message){
            payload := string(message.Payload())
            parts := strings.SplitN(payload, "\n", 2)
            if len(parts) == 1 {
                notify(s, parts[0], "")
            } else {
                notify(s, parts[0], parts[1])
            }
        })
        if !t.WaitTimeout(timeout) {
            return errors.New("MQTT Subscribe timed out")
        } else if t.Error() != nil {
            return t.Error()
        }
    }

    return nil
}


func unsubscribe() {
    if mqttClient != nil {
        for _, sub := range(config.Subscriptions) {
            if sub.Topic != "" {
                log.Printf("Unsubscribe from %s", sub.Topic)
                mqttClient.Unsubscribe(sub.Topic)
            }
        }
    }
}


// Config ---------------------------------------------------------------------


type Config struct {
    Host            string              `json:"host"`
    Port            int                 `json:"port"`
    User            string              `json:"user"`
    Pass            string              `json:"pass"`
    Timeout         int                 `json:"timeout"`
    Icon            string              `json:"icon"`
    Subscriptions   []*Subscription    `json:"subscriptions"`
}


type Subscription struct {
    Topic   string  `json:"topic"`
    Title   string  `json:"title"`
    Body    string  `json:"body"`
    Icon    string  `json:"icon"`

}


func loadConfig() error {
    // initialize with defaults
    config = &Config{
        Host: "localhost",
        Port: 1883,
        User: "",
        Pass: "",
        Timeout: 5,
        Icon: "dialog-information",
        Subscriptions: []*Subscription{},
    }

    currentUser, err := user.Current()
    if err != nil {
        return err
    }

    path := filepath.Join(currentUser.HomeDir, ".config", APPNAME + ".json")
    f, err := os.Open(path)
    if os.IsNotExist(err) {
        log.Printf("No config file found at %v, using defaults", path)
        return nil
    } else if err != nil {
        return err
    }
    defer f.Close()

    decoder := json.NewDecoder(f)
    for {
        if err := decoder.Decode(&config); err == io.EOF {
            break
        } else if err != nil {
            return err
        }
    }

    return nil
}
