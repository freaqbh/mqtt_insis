package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand" // Masih digunakan untuk simulasi waktu proses
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	broker               = "ssl://localhost:8883"
	requestTopic         = "app/request/job"
	requesterStatusTopic = "app/status/requester"
	clientID             = "go-mqtt-interactive-sub-no-debug" // Client ID diperbarui
	caCertFile           = "../certs/ca.crt"
	username             = "testuser"
	password             = "testuser" // GANTI DENGAN PASSWORD YANG SESUAI
)

// Structs Payload
type RequestPayload struct {
	Task          string    `json:"task"`
	ResponseTopic string    `json:"response_topic"`
	CorrelationID string    `json:"correlation_id"`
	Timestamp     time.Time `json:"timestamp"`
	QoSUsed       byte      `json:"qos_used"`
}

type ResponsePayload struct {
	Result        string    `json:"result"`
	CorrelationID string    `json:"correlation_id"`
	Timestamp     time.Time `json:"timestamp"`
}

type StatusPayload struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	ClientID  string    `json:"client_id"`
}

// Fungsi NewTLSConfig
func NewTLSConfig(caFile string) (*tls.Config, error) {
	certpool := x509.NewCertPool()
	pemCerts, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("gagal baca CA certificate %s: %w", caFile, err)
	}
	if !certpool.AppendCertsFromPEM(pemCerts) {
		return nil, fmt.Errorf("gagal append CA certificate dari %s ke pool", caFile)
	}
	return &tls.Config{
		RootCAs:    certpool,
		ServerName: "localhost",
	}, nil
}

// Handler Koneksi
var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Println("SUBSCRIBER: Terautentikasi & terhubung dengan aman (MQTTS v5, Interaktif)")

	if token := client.Subscribe(requestTopic, 2, requestMessageHandler); token.Wait() && token.Error() != nil {
		log.Fatalf("SUBSCRIBER: Gagal subscribe ke request topic '%s': %v", requestTopic, token.Error())
	} else {
		log.Printf("SUBSCRIBER: Berhasil subscribe ke request topic: %s dengan QoS 2", requestTopic)
	}

	if token := client.Subscribe(requesterStatusTopic, 1, statusMessageHandler); token.Wait() && token.Error() != nil {
		log.Printf("SUBSCRIBER: Gagal subscribe ke status topic '%s': %v", requesterStatusTopic, token.Error())
	} else {
		log.Printf("SUBSCRIBER: Berhasil subscribe ke status topic: %s", requesterStatusTopic)
	}
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Printf("SUBSCRIBER: Koneksi hilang: %v. Ini bisa disebabkan oleh masalah jaringan atau timeout Keep Alive.", err)
}

// Handler Pesan Request
var requestMessageHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	log.Printf("SUBSCRIBER (Request): Terima pesan (Actual QoS: %d, Topic: '%s')", msg.Qos(), msg.Topic())
	var reqPayload RequestPayload
	if err := json.Unmarshal(msg.Payload(), &reqPayload); err != nil {
		log.Printf("SUBSCRIBER (Request): Gagal unmarshal request: %v. Payload: %s", err, string(msg.Payload()))
		return
	}
	log.Printf("SUBSCRIBER (Request): Proses request (Original Pub QoS: %d) CID [%s]: Task '%s'.",
		reqPayload.QoSUsed, reqPayload.CorrelationID, reqPayload.Task)

	// Simulasi waktu proses yang sedikit bervariasi (1 atau 2 detik)
	sleepDurationSeconds := rand.Intn(2) + 1 // Hasilnya 1 atau 2
	time.Sleep(time.Duration(sleepDurationSeconds) * time.Second)

	responseText := fmt.Sprintf("Processed task '%s' by Subscriber (Orig Pub QoS: %d, Recv QoS: %d)",
		reqPayload.Task, reqPayload.QoSUsed, msg.Qos())
	respPayload := ResponsePayload{
		Result:        responseText,
		CorrelationID: reqPayload.CorrelationID,
		Timestamp:     time.Now(),
	}
	payloadBytes, errJson := json.Marshal(respPayload)
	if errJson != nil {
		log.Printf("SUBSCRIBER (Request): Gagal marshal response CID [%s]: %v", reqPayload.CorrelationID, errJson)
		return
	}
	if reqPayload.ResponseTopic == "" {
		log.Printf("SUBSCRIBER (Request): Warning - Tidak ada ResponseTopic CID [%s]", reqPayload.CorrelationID)
		return
	}
	pubToken := client.Publish(reqPayload.ResponseTopic, 1, false, payloadBytes)
	if pubToken.Error() != nil {
		log.Printf("SUBSCRIBER (Request): Gagal publish response CID [%s]: %v", reqPayload.CorrelationID, pubToken.Error())
	} else {
		log.Printf("SUBSCRIBER (Request): Kirim response QoS 1 CID [%s]", reqPayload.CorrelationID)
	}
}

// Handler Pesan Status
var statusMessageHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	var statusPayload StatusPayload
	if err := json.Unmarshal(msg.Payload(), &statusPayload); err != nil {
		log.Printf("SUBSCRIBER (Status): Gagal unmarshal status: %v. Payload: %s", err, string(msg.Payload()))
		return
	}
	log.Printf("SUBSCRIBER (Status): Terima status dari '%s': Client '%s' adalah '%s'. Retained: %t, QoS: %d",
		msg.Topic(), statusPayload.ClientID, statusPayload.Status, msg.Retained(), msg.Qos())
}


func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	// Paho DEBUG dan WARN logging dinonaktifkan (dikomentari)
	// mqtt.DEBUG = log.New(os.Stdout, "[PAHO-DEBUG] ", log.LstdFlags|log.Lshortfile)
	// mqtt.WARN = log.New(os.Stdout, "[PAHO-WARN]  ", log.LstdFlags|log.Lshortfile)

	tlsConfig, err := NewTLSConfig(caCertFile)
	if err != nil {
		log.Fatalf("SUBSCRIBER: Gagal membuat TLS config: %v", err)
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(clientID)
	opts.SetTLSConfig(tlsConfig)
	opts.SetProtocolVersion(5)
	opts.SetUsername(username)
	opts.SetPassword(password)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetKeepAlive(10 * time.Second) // Keep Alive 10 detik

	client := mqtt.NewClient(opts)
	log.Printf("SUBSCRIBER: Mencoba menghubungkan dengan username '%s'...", username)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(fmt.Errorf("SUBSCRIBER: Gagal connect ke broker MQTTS: %w", token.Error()))
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nSUBSCRIBER: Sinyal shutdown diterima.")
	fmt.Println("SUBSCRIBER: Unsubscribe dan disconnect...")
	if token := client.Unsubscribe(requestTopic, requesterStatusTopic); token.WaitTimeout(3*time.Second) && token.Error() != nil {
		fmt.Printf("SUBSCRIBER: Gagal unsubscribe: %v\n", token.Error())
	}
	client.Disconnect(250)
	fmt.Println("SUBSCRIBER: Keluar dengan bersih.")
}

func init() {
    // Inisialisasi seed untuk math/rand agar lebih acak jika diperlukan
    // Untuk Go versi sebelum 1.20, ini penting. Untuk 1.20+, tidak wajib untuk global rand.
    rand.New(rand.NewSource(time.Now().UnixNano()))
}