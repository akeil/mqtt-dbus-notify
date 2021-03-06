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

	mqtt "github.com/eclipse/paho.mqtt.golang"
	dbus "github.com/godbus/dbus"
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
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

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
	_ = <-signals
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

	return call.Err
}

// MQTT -----------------------------------------------------------------------

// Connect to the MQTT broker from config
func connectMQTT() error {
	log.Println("Connect to MQTT ...")
	opts := mqtt.NewClientOptions()

	var scheme string
	if config.Secure {
		scheme = "tcps"
	} else {
		scheme = "tcp"
	}
	url := fmt.Sprintf("%v://%v:%v", scheme, config.Host, config.Port)
	opts.AddBroker(url)

	if config.Username != "" {
		opts.SetUsername(config.Username)
		opts.SetPassword(config.Password)
	}

	opts.SetConnectionLostHandler(onMQTTConnectionLost)
	opts.SetOnConnectHandler(onMQTTConnected)

	hostname, err := os.Hostname()
	if err == nil {
		opts.SetClientID(APPNAME + "-" + hostname)
		opts.SetCleanSession(false) // don't lose subscriptions on reconnect
	}

	mqttClient = mqtt.NewClient(opts) // global

	timeout := time.Duration(config.Timeout) * time.Second
	t := mqttClient.Connect()
	if !t.WaitTimeout(timeout) {
		return errors.New("MQTT Connect timed out")
	}
	return t.Error()
}

func onMQTTConnectionLost(client mqtt.Client, err error) {
	log.Println("MQTT connection lost")
}

func onMQTTConnected(client mqtt.Client) {
	log.Println("MQTT connected")
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
			s.Trigger(m.Topic(), string(m.Payload()))
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

const tplTitle = "title"
const tplBody = "body"

// Configuration for a single MQTT subscription.
type Subscription struct {
	Topic           string                        `json:"topic"`
	Title           string                        `json:"title"`
	Body            string                        `json:"body"`
	Icon            string                        `json:"icon"`
	cachedTemplates map[string]*template.Template `json:"-"`
}

// Called for each incoming MQTT message that matches this subscription.
func (s *Subscription) Trigger(topic, payload string) {
	title, body, err := s.createTitleAndBody(topic, payload)
	if err != nil {
		log.Printf("ERROR: Failed to create notification: %v", err)
		return
	}

	icon := s.Icon
	if icon == "" {
		icon = config.Icon
	}
	notify(title, body, icon)
}

// Create title and body for a notification.
// Either from default (title=first line, body=subsequent lines)
// or by filling the respective templates from configuration.
func (s *Subscription) createTitleAndBody(topic, payload string) (string, string, error) {
	title := ""
	body := ""
	useTemplates := s.Title != "" || s.Body != ""

	if useTemplates {
		return s.fillTemplates(topic, payload)
	} else {
		parts := strings.SplitN(payload, "\n", 2)
		title = parts[0]
		if len(parts) > 1 {
			body = parts[1]
		}
	}

	return title, body, nil
}

// Prepare (parse) templates if not already cached.
func (s *Subscription) prepareTemplates() error {
	if s.cachedTemplates != nil {
		return nil
	}

	var err error
	templates := []string{tplTitle, tplBody}
	s.cachedTemplates = make(map[string]*template.Template, len(templates))

	for _, name := range templates {
		tpl := template.New(name)
		var raw string
		if name == tplTitle {
			raw = s.Title
		} else {
			raw = s.Body
		}
		_, err = tpl.Parse(raw)
		if err != nil {
			return err
		}
		s.cachedTemplates[name] = tpl
	}
	return nil
}

func (s *Subscription) fillTemplates(topic, payload string) (string, string, error) {
	err := s.prepareTemplates()
	if err != nil {
		return "", "", err
	}

	var title, body string
	ctx := NewTemplateContext(topic, payload)

	for name, tpl := range s.cachedTemplates {
		buf := new(bytes.Buffer)
		err = tpl.Execute(buf, &ctx)
		if err != nil {
			return "", "", err
		}
		if name == tplTitle {
			title = buf.String()
		} else {
			body = buf.String()
		}
	}

	return title, body, nil
}

type TemplateContext struct {
	payload string
	parts   []string
}

func NewTemplateContext(topic, payload string) TemplateContext {
	return TemplateContext{
		payload: payload,
		parts:   strings.Split(topic, "/"),
	}
}

func (t *TemplateContext) Topic(index int) (string, error) {
	if index < 0 || index > len(t.parts) {
		return "", errors.New("Invalid topic index")
	}

	return t.parts[index], nil
}

func (t *TemplateContext) String() string {
	return t.payload
}

// Config ---------------------------------------------------------------------

// Configuration options
type Config struct {
	Host          string          `json:"host"`
	Port          int             `json:"port"`
	Username      string          `json:"username"`
	Password      string          `json:"password"`
	Secure        bool            `json:"secure"`
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
		Username:      "",
		Password:      "",
		Secure:        false,
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
