package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	broker   = "tcp://localhost:1883"
	clientID = "go-mqtt-subscriber"
	topic    = "mqtt-testing"
)

var mqttMsgChan = make(chan mqtt.Message)

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	mqttMsgChan <- msg
}

func processMsg(ctx context.Context, input <-chan mqtt.Message) chan mqtt.Message {
	out := make(chan mqtt.Message)
	go func() {
		defer close(out)
		for {
			select {
			case msg, ok := <-input:
				if !ok {
					return
				}
				fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
				out <- msg
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("Connected to MQTT Broker")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("Connection lost: %v", err)
}

func main() {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(clientID)
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		finalChan := processMsg(ctx, mqttMsgChan)
		for range finalChan {
			
		}
	}()

	// Subscribe to the topic
	token := client.Subscribe(topic, 1, nil)
	token.Wait()
	fmt.Printf("Subscribed to topic: %s\n", topic)

	// Wait for interrupt signal to gracefully shutdown the subscriber
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	cancel()

	// Unsubscribe and disconnect
	fmt.Println("Unsubscribing and disconnecting...")
	client.Unsubscribe(topic)
	client.Disconnect(250)

	// Wait for the goroutine to finish
	wg.Wait()
	fmt.Println("Goroutine terminated, exiting...")
}
