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

    config, err := readConfig()
    if err != nil {
        return err
    }

    err = connectDBus(config)
    if err != nil {
        return err
    }
    defer disconnectDBus()

    err = connectMQTT(config)
    if err != nil {
        return err
    }
    defer disconnectMQTT()

    err = subscribe(config)
    if err != nil {
        return err
    }
    defer unsubscribe(config)

    // blocks until SIGINT
    _ = <- s
    return nil
}


func connectDBus(config *Config) error {
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


func notify(title string, body string) error {
    call := notifications.Call(NOTIFY_METHOD, 0, APPNAME, uint32(0), "",
        title, body,
        []string{}, map[string]dbus.Variant{}, int32(7000))
    if call.Err != nil {
        return call.Err
    }
    return nil
}


func connectMQTT(config *Config) error {
    log.Println("Connect to MQTT ...")
    uri := fmt.Sprintf("tcp://%v:%v", config.Host, config.Port)
    opts := mqtt.NewClientOptions()
    opts.AddBroker(uri)
    //opts.SetClientID("mqtt-dbus-notify-HOSTNAME")
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


func subscribe(config *Config) error {
    timeout := time.Duration(config.Timeout) * time.Second
    qos := byte(0)

    for _, topic := range(config.Topics) {
        log.Printf("Subscribe to %s", topic)
        t := mqttClient.Subscribe(topic, qos, func(client mqtt.Client, message mqtt.Message){
            payload := string(message.Payload())
            parts := strings.SplitN(payload, "\n", 2)
            if len(parts) == 1 {
                notify(parts[0], "")
            } else {
                notify(parts[0], parts[1])
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


func unsubscribe(config *Config) {
    if mqttClient != nil {
        for _, topic := range(config.Topics) {
            log.Printf("Unsubscribe from %s", topic)
            mqttClient.Unsubscribe(topic)
        }
    }
}


type Config struct {
    Host    string      `json:"host"`
    Port    int         `json:"port"`
    User    string      `json:"user"`
    Pass    string      `json:"pass"`
    Timeout int         `json:"timeout"`
    Topics  []string    `json:"topics"`
}


func readConfig() (*Config, error) {
    // initialize with defaults
    config := &Config{
        Host: "localhost",
        Port: 1883,
        User: "",
        Pass: "",
        Timeout: 5,
        Topics: []string{},
    }
    // TODO read from JSON, map to config
    currentUser, err := user.Current()
    if err != nil {
        return config, err
    }

    path := filepath.Join(currentUser.HomeDir, ".config", APPNAME + ".json")
    f, err := os.Open(path)
    if os.IsNotExist(err) {
        log.Printf("No config file found at %v, using defaults", path)
        return config, nil
    } else if err != nil {
        return config, err
    }
    defer f.Close()

    decoder := json.NewDecoder(f)
    for {
        if err := decoder.Decode(&config); err == io.EOF {
            break
        } else if err != nil {
            return config, err
        }
    }

    return config, nil
}
