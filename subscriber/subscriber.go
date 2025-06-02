package main

import (
    "crypto/tls"
    "crypto/x509"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "math/rand"
    "os"
    "os/signal"
    "sync/atomic"
    "syscall"
    "time"

    mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
    broker               = "ssl://localhost:8883"
    requestTopic         = "app/request/job"
    requesterStatusTopic = "app/status/requester"
    clientID             = "go-mqtt-interactive-sub-no-debug"
    caCertFile           = "../certs/ca.crt"
    username             = "testuser"
    password             = "testuser"
    
    // Default values for new features
    defaultKeepAliveInterval = 10   // Keep-alive interval in seconds
)

// Monitoring ping-pong statistics
var lastPingTime time.Time
var pingCount uint64
var pongCount uint64

// Structs Payload
type RequestPayload struct {
    Task           string    `json:"task"`
    ResponseTopic  string    `json:"response_topic"`
    CorrelationID  string    `json:"correlation_id"`
    Timestamp      time.Time `json:"timestamp"`
    QoSUsed        byte      `json:"qos_used"`
    ExpiryInterval uint32    `json:"expiry_interval,omitempty"` // We'll handle this manually
}

type ResponsePayload struct {
    Result        string    `json:"result"`
    CorrelationID string    `json:"correlation_id"`
    Timestamp     time.Time `json:"timestamp"`
    ExpiryTime    time.Time `json:"expiry_time,omitempty"` // Added for v3.1.1 compatibility
}

type StatusPayload struct {
    Status        string    `json:"status"`
    Timestamp     time.Time `json:"timestamp"`
    ClientID      string    `json:"client_id"`
    ExpiryTime    time.Time `json:"expiry_time,omitempty"` // Added for v3.1.1 compatibility
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
    log.Println("SUBSCRIBER: Terautentikasi & terhubung dengan aman (MQTTS, Interaktif)")

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
    
    // Simulate ping for statistics in v3.1.1
    go func(c mqtt.Client) {
        for {
            time.Sleep(defaultKeepAliveInterval * time.Second)
            if !c.IsConnected() {
                return
            }
            // Record ping event
            lastPingTime = time.Now()
            atomic.AddUint64(&pingCount, 1)
            log.Printf("SUBSCRIBER: PING sent to broker (#%d) - simulated", atomic.LoadUint64(&pingCount))
            
            // Simulate pong after a short delay
            time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)
            elapsed := time.Since(lastPingTime)
            atomic.AddUint64(&pongCount, 1)
            log.Printf("SUBSCRIBER: PONG received from broker (#%d) - Latency: %v - simulated", 
                atomic.LoadUint64(&pongCount), elapsed)
        }
    }(client)
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
    log.Printf("SUBSCRIBER: Koneksi hilang: %v. Ini bisa disebabkan oleh masalah jaringan atau timeout Keep Alive.", err)
}

// Handler Pesan Request with expiry handling
var requestMessageHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
    log.Printf("SUBSCRIBER (Request): Terima pesan (Actual QoS: %d, Topic: '%s')", msg.Qos(), msg.Topic())
    
    var reqPayload RequestPayload
    if err := json.Unmarshal(msg.Payload(), &reqPayload); err != nil {
        log.Printf("SUBSCRIBER (Request): Gagal unmarshal request: %v. Payload: %s", err, string(msg.Payload()))
        return
    }
    
    log.Printf("SUBSCRIBER (Request): Proses request (Original Pub QoS: %d) CID [%s]: Task '%s'.",
        reqPayload.QoSUsed, reqPayload.CorrelationID, reqPayload.Task)
    
    // Check if the message has expired from client-specified interval
    if reqPayload.ExpiryInterval > 0 {
        elapsedSinceTimestamp := time.Since(reqPayload.Timestamp).Seconds()
        if elapsedSinceTimestamp > float64(reqPayload.ExpiryInterval) {
            log.Printf("SUBSCRIBER (Request): Message with CID [%s] has expired (%.2f sec elapsed, limit %d sec). Dropping.",
                reqPayload.CorrelationID, elapsedSinceTimestamp, reqPayload.ExpiryInterval)
            return
        }
        log.Printf("SUBSCRIBER (Request): Message with CID [%s] still valid (%.2f sec elapsed, limit %d sec)",
            reqPayload.CorrelationID, elapsedSinceTimestamp, reqPayload.ExpiryInterval)
    }

    // Simulasi waktu proses yang sedikit bervariasi (1 atau 2 detik)
    sleepDurationSeconds := rand.Intn(2) + 1 // Hasilnya 1 atau 2
    time.Sleep(time.Duration(sleepDurationSeconds) * time.Second)

    responseText := fmt.Sprintf("Processed task '%s' by Subscriber (Orig Pub QoS: %d, Recv QoS: %d)",
        reqPayload.Task, reqPayload.QoSUsed, msg.Qos())
    
    // Calculate expiry time for response
    var expiryTime time.Time
    if reqPayload.ExpiryInterval > 0 {
        remainingExpiry := reqPayload.ExpiryInterval
        if remainingExpiry > uint32(sleepDurationSeconds) {
            remainingExpiry -= uint32(sleepDurationSeconds)
        } else {
            remainingExpiry = 1 // Minimum 1 second
        }
        expiryTime = time.Now().Add(time.Duration(remainingExpiry) * time.Second)
    }
    
    respPayload := ResponsePayload{
        Result:        responseText,
        CorrelationID: reqPayload.CorrelationID,
        Timestamp:     time.Now(),
        ExpiryTime:    expiryTime,
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
    
    // Publish using standard method
    pubToken := client.Publish(reqPayload.ResponseTopic, 1, false, payloadBytes)
    
    if pubToken.Error() != nil {
        log.Printf("SUBSCRIBER (Request): Gagal publish response CID [%s]: %v", reqPayload.CorrelationID, pubToken.Error())
    } else {
        if !expiryTime.IsZero() {
            remainingSecs := time.Until(expiryTime).Seconds()
            log.Printf("SUBSCRIBER (Request): Kirim response QoS 1 CID [%s] dengan expiry %.1f sec", 
                reqPayload.CorrelationID, remainingSecs)
        } else {
            log.Printf("SUBSCRIBER (Request): Kirim response QoS 1 CID [%s]", reqPayload.CorrelationID)
        }
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
    
    // If status has expiry, log it
    if !statusPayload.ExpiryTime.IsZero() {
        remainingSecs := time.Until(statusPayload.ExpiryTime).Seconds()
        if remainingSecs > 0 {
            log.Printf("SUBSCRIBER (Status): Status expires in %.1f seconds", remainingSecs)
        } else {
            log.Printf("SUBSCRIBER (Status): Status has already expired")
        }
    }
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
    opts.SetUsername(username)
    opts.SetPassword(password)
    opts.OnConnect = connectHandler
    opts.OnConnectionLost = connectLostHandler
    opts.SetAutoReconnect(true)
    opts.SetMaxReconnectInterval(10 * time.Second)
    opts.SetKeepAlive(defaultKeepAliveInterval)
    
    // We'll simulate ping-pong in the connect handler

    client := mqtt.NewClient(opts)
    log.Printf("SUBSCRIBER: Mencoba menghubungkan dengan username '%s'...", username)
    if token := client.Connect(); token.Wait() && token.Error() != nil {
        panic(fmt.Errorf("SUBSCRIBER: Gagal connect ke broker MQTTS: %w", token.Error()))
    }

    // Start a goroutine to periodically show ping-pong statistics
    stopStatsTicker := make(chan bool)
    go func() {
        ticker := time.NewTicker(60 * time.Second)
        defer ticker.Stop()
        
        for {
            select {
            case <-ticker.C:
                log.Printf("SUBSCRIBER: Ping-Pong statistik - Sent: %d, Received: %d", 
                    atomic.LoadUint64(&pingCount), atomic.LoadUint64(&pongCount))
            case <-stopStatsTicker:
                return
            }
        }
    }()

    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    // Stop the stats ticker
    close(stopStatsTicker)

    fmt.Println("\nSUBSCRIBER: Sinyal shutdown diterima.")
    fmt.Println("SUBSCRIBER: Unsubscribe dan disconnect...")
    if token := client.Unsubscribe(requestTopic, requesterStatusTopic); token.WaitTimeout(3*time.Second) && token.Error() != nil {
        fmt.Printf("SUBSCRIBER: Gagal unsubscribe: %v\n", token.Error())
    }
    client.Disconnect(250)
    fmt.Println("SUBSCRIBER: Keluar dengan bersih.")
}

func init() {
    rand.New(rand.NewSource(time.Now().UnixNano()))
    lastPingTime = time.Time{} // Initialize as zero time
}