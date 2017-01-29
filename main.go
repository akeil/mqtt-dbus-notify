package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	dbus "github.com/godbus/dbus"
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
var subscribed = make([]string, 0)

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
	_ = <-s
	return nil
}

// DBUS -----------------------------------------------------------------------

// Connect to the D-Bus session bus
// and initialize a proxy object for the notifications service.
func connectDBus() error {
	log.Println("Connect to DBus...")
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}

	dbusConn = conn                                        // global
	notifications = dbusConn.Object(DESTINATION, OBJ_PATH) // global

	return nil
}

// Disconnect from D-Bus session bus.
func disconnectDBus() {
	if dbusConn != nil {
		dbusConn.Close()
		log.Println("Disconnected from DBus")
	}
}

// Send a notifcation through the D-Bus notifications service.
func notify(title, body, icon string) error {
	call := notifications.Call(NOTIFY_METHOD, 0, APPNAME, uint32(0),
		icon, title, body,
		[]string{}, map[string]dbus.Variant{}, int32(7000))
	if call.Err != nil {
		return call.Err
	}
	return nil
}

// MQTT -----------------------------------------------------------------------

// Connect to the MQTT broker from config
func connectMQTT() error {
	log.Println("Connect to MQTT ...")
	uri := fmt.Sprintf("tcp://%v:%v", config.Host, config.Port)
	opts := mqtt.NewClientOptions()
	opts.AddBroker(uri)

	hostname, err := os.Hostname()
	if err == nil {
		opts.SetClientID(APPNAME + "-" + hostname)
	}
	mqttClient = mqtt.NewClient(opts) // global

	timeout := time.Duration(config.Timeout) * time.Second
	t := mqttClient.Connect()
	if !t.WaitTimeout(timeout) {
		return errors.New("MQTT Connect timed out")
	}
	return t.Error()
}

// Disconnect from the MQTT broker
func disconnectMQTT() {
	if mqttClient != nil {
		if mqttClient.IsConnected() {
			mqttClient.Disconnect(250) // 250 millis cleanup time
			log.Println("Disconnected from MQTT")
		}
	}
}

// Subscribe to all configured topics.
// Stores successful subscriptions in global `subscriptions` variable.
func subscribe() error {
	if len(config.Subscriptions) == 0 {
		log.Println("WARNING: No subscriptions configured.")
		return nil
	}

	timeout := time.Duration(config.Timeout) * time.Second
	qos := byte(0)

	for _, sub := range config.Subscriptions {
		if sub.Topic == "" {
			log.Println("WARNING: Ignoring subscription without topic.")
			continue
		}
		log.Printf("Subscribe to %s", sub.Topic)
		s := sub // local var for scope
		t := mqttClient.Subscribe(sub.Topic, qos, func(c mqtt.Client, m mqtt.Message) {
			s.Trigger(m.Payload())
		})

		if !t.WaitTimeout(timeout) {
			return errors.New("MQTT Subscribe timed out")
		} else if t.Error() != nil {
			return t.Error()
		}

		subscribed = append(subscribed, sub.Topic)
	}

	return nil
}

// Unsubscribe from all previously subscribed topics.
func unsubscribe() {
	if mqttClient != nil {
		for _, topic := range subscribed {
			log.Printf("Unsubscribe from %s", topic)
			mqttClient.Unsubscribe(topic)
		}
	}
}

// Subscriptions --------------------------------------------------------------

// Configuration for a single MQTT subscription.
type Subscription struct {
	Topic string `json:"topic"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon"`
}

// Called for each incoming MQTT message that matches this subscription.
func (s *Subscription) Trigger(payload []byte) {
	text := string(payload)
	title, body := s.createTitleAndBody(text)
	icon := config.Icon
	if s.Icon != "" {
		icon = s.Icon
	}
	notify(title, body, icon)
}

// Create title and body for a notification.
// Either from default (title=first line, body=subsequent lines)
// or by filling the respective templates from configuration.
func (s *Subscription) createTitleAndBody(text string) (string, string) {
	title := ""
	body := ""
	useTemplates := s.Title != "" || s.Body != ""

	if useTemplates {
		var err0, err1 error
		body, err0 = s.template("Body", text)
		title, err1 = s.template("Title", text)
		if err0 != nil || err1 != nil {
			log.Println("ERROR: Failed to parse template")
		}

	} else {
		parts := strings.SplitN(text, "\n", 2)
		title = parts[0]
		if len(parts) > 1 {
			body = parts[1]
		}
	}

	return title, body
}

// Fill the given template - "Title" or "Body" with the given text.
func (s *Subscription) template(which string, text string) (string, error) {
	var templateString string
	if which == "Title" {
		templateString = s.Title
	} else if which == "Body" {
		templateString = s.Body
	} else {
		return "", errors.New("Invalid template name")
	}

	t := template.New(which)
	_, err := t.Parse(templateString)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	err = t.Execute(buf, text)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// Config ---------------------------------------------------------------------

// Configuration options
type Config struct {
	Host          string          `json:"host"`
	Port          int             `json:"port"`
	User          string          `json:"user"`
	Pass          string          `json:"pass"`
	Timeout       int             `json:"timeout"`
	Icon          string          `json:"icon"`
	Subscriptions []*Subscription `json:"subscriptions"`
}

// Read configuration from the default path and set global `config` variable.
func loadConfig() error {
	// initialize with defaults
	config = &Config{
		Host:          "localhost",
		Port:          1883,
		User:          "",
		Pass:          "",
		Timeout:       5,
		Icon:          "dialog-information",
		Subscriptions: []*Subscription{},
	}

	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	path := filepath.Join(currentUser.HomeDir, ".config", APPNAME+".json")
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
