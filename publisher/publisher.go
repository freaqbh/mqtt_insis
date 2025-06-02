package main

import (
    "bufio"
    "crypto/tls"
    "crypto/x509"
    "encoding/json"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "math/rand"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "sync/atomic"
    "syscall"
    "time"

    mqtt "github.com/eclipse/paho.mqtt.golang"
    "github.com/google/uuid"
)

const (
    broker               = "ssl://localhost:8883"
    requestTopic         = "app/request/job"
    requesterStatusTopic = "app/status/requester"
    clientID             = "go-mqtt-interactive-pub-fix2"
    caCertFile           = "../certs/ca.crt"
    username             = "testuser"
    password             = "testuser"
    
    // Default values for new features
    defaultMessageExpiryInterval = 60 // Message expiry in seconds
    defaultKeepAliveInterval     = 10 // Keep-alive interval in seconds
)

var responseTopic = fmt.Sprintf("app/response/job/%s", clientID)
var defaultRequestQoS byte = 1

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
    log.Println("PUBLISHER: Terautentikasi & terhubung dengan aman (MQTTS, Interaktif)")
    
    if token := client.Subscribe(responseTopic, 1, responseMessageHandler); token.Wait() && token.Error() != nil {
        log.Printf("PUBLISHER: Gagal subscribe ke response topic '%s': %v", responseTopic, token.Error())
    } else {
        log.Printf("PUBLISHER: Berhasil subscribe ke response topic: %s", responseTopic)
    }
    
    publishRetainedMessage(client, requesterStatusTopic, StatusPayload{
        Status: "online", 
        Timestamp: time.Now(), 
        ClientID: clientID,
    }, 1, true)
    
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
            log.Printf("PUBLISHER: PING sent to broker (#%d) - simulated", atomic.LoadUint64(&pingCount))
            
            // Simulate pong after a short delay
            time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)
            elapsed := time.Since(lastPingTime)
            atomic.AddUint64(&pongCount, 1)
            log.Printf("PUBLISHER: PONG received from broker (#%d) - Latency: %v - simulated", 
                atomic.LoadUint64(&pongCount), elapsed)
        }
    }(client)
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
    log.Printf("PUBLISHER: Koneksi hilang: %v. Ini bisa disebabkan oleh masalah jaringan atau timeout Keep Alive.", err)
}

// Handler Pesan Respons
var responseMessageHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
    var respPayload ResponsePayload
    if err := json.Unmarshal(msg.Payload(), &respPayload); err != nil {
        log.Printf("PUBLISHER: Gagal unmarshal response: %v", err)
        return
    }
    
    log.Printf("PUBLISHER: Terima respons (QoS: %d) untuk CID [%s]: '%s'", 
        msg.Qos(), respPayload.CorrelationID, respPayload.Result)
    
    // If response has expiry, log it
    if !respPayload.ExpiryTime.IsZero() {
        remainingSecs := time.Until(respPayload.ExpiryTime).Seconds()
        if remainingSecs > 0 {
            log.Printf("PUBLISHER: Response expires in %.1f seconds", remainingSecs)
        } else {
            log.Printf("PUBLISHER: Response has already expired")
        }
    }
}

// Enhanced publishMessage function with manual expiry tracking
func publishMessage(client mqtt.Client, topic string, payload interface{}, qos byte, 
    retained bool, expiryInterval uint32) {
    
    // For payload types that support expiry, we can set expiry time
    switch p := payload.(type) {
    case *RequestPayload:
        p.ExpiryInterval = expiryInterval
    case *StatusPayload:
        if expiryInterval > 0 {
            p.ExpiryTime = time.Now().Add(time.Duration(expiryInterval) * time.Second)
        }
    }
    
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        log.Printf("PUBLISHER: Gagal marshal payload untuk topik %s: %v", topic, err)
        return
    }
    
    token := client.Publish(topic, qos, retained, payloadBytes)
    
    if token.WaitTimeout(3 * time.Second) {
        if token.Error() != nil {
            log.Printf("PUBLISHER: Gagal publish ke topik %s: %v", topic, token.Error())
        } else {
            log.Printf("PUBLISHER: Berhasil publish ke topik %s (QoS: %d, Retained: %t, Expiry: %d)",
                topic, qos, retained, expiryInterval)
        }
    } else {
        log.Printf("PUBLISHER: Timeout saat publish ke topik %s", topic)
    }
}

// Wrapper for status messages
func publishRetainedMessage(client mqtt.Client, topic string, statusPayload StatusPayload, 
    qos byte, retained bool) {
    
    log.Printf("PUBLISHER: Mempublish status '%s' ke topik '%s' (Retained: %t, QoS: %d)",
        statusPayload.Status, topic, retained, qos)
    
    // Status messages have longer expiry as they're important
    if retained {
        statusPayload.ExpiryTime = time.Now().Add(3600 * time.Second) // 1 hour expiry
    }
    
    publishMessage(client, topic, &statusPayload, qos, retained, 3600) // 1 hour expiry
}

// Fungsi untuk membaca input user
func readInput(bReader *bufio.Reader, prompt string) (string, error) {
    fmt.Print(prompt)
    input, err := bReader.ReadString('\n')
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(input), nil
}

// Manual ping function for testing
func sendManualPing(client mqtt.Client) {
    // Simulate a ping
    lastPingTime = time.Now()
    atomic.AddUint64(&pingCount, 1)
    log.Printf("PUBLISHER: Manual PING sent to broker (#%d) - simulated", atomic.LoadUint64(&pingCount))
    
    // Simulate pong after a short delay
    time.Sleep(time.Duration(rand.Intn(300)) * time.Millisecond)
    elapsed := time.Since(lastPingTime)
    atomic.AddUint64(&pongCount, 1)
    log.Printf("PUBLISHER: PONG received for manual ping (#%d) - Latency: %v - simulated", 
        atomic.LoadUint64(&pongCount), elapsed)
}

func main() {
    log.SetFlags(log.LstdFlags | log.Lshortfile)
    
    // Uncomment for debug logging
    // mqtt.DEBUG = log.New(os.Stdout, "[PAHO-DEBUG] ", log.LstdFlags|log.Lshortfile)
    // mqtt.WARN = log.New(os.Stdout, "[PAHO-WARN]  ", log.LstdFlags|log.Lshortfile)

    tlsConfig, err := NewTLSConfig(caCertFile)
    if err != nil {
        log.Fatalf("PUBLISHER: Gagal membuat TLS config: %v", err)
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
    log.Printf("PUBLISHER: Mencoba menghubungkan dengan username '%s'...", username)
    if token := client.Connect(); token.Wait() && token.Error() != nil {
        panic(fmt.Errorf("PUBLISHER: Gagal connect ke broker MQTTS: %w", token.Error()))
    }

    mainReader := bufio.NewReader(os.Stdin)
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    keepRunning := true

    // Current settings
    messageExpiryInterval := defaultMessageExpiryInterval

    go func() {
        <-sigChan
        log.Println("\nPUBLISHER: Sinyal interrupt diterima, memproses keluar...")
        keepRunning = false
    }()

    for keepRunning {
        fmt.Printf("\n--- Pilihan Aksi Publisher ---\n")
        fmt.Printf("1. Kirim Request (QoS saat ini: %d, Expiry: %d detik)\n", 
            defaultRequestQoS, messageExpiryInterval)
        fmt.Println("2. Ubah QoS Default untuk Request Berikutnya")
        fmt.Println("3. Publish Retained Status (misal: 'sibuk')")
        fmt.Println("4. Hapus Retained Status (kirim payload kosong)")
        fmt.Println("5. Diam selama 25 detik (Keep-Alive=10 detik akan aktif di background)")
        fmt.Println("6. Lihat statistik Ping-Pong")
        fmt.Println("7. Ubah Message Expiry Interval")
        fmt.Println("8. Kirim Ping manual")
        fmt.Println("0. Keluar")
        fmt.Print("Masukkan pilihan Anda: ")

        choiceStr, errRead := mainReader.ReadString('\n')
        if errRead != nil {
            if !keepRunning || errRead == io.EOF {
                log.Println("PUBLISHER: Error membaca input atau sinyal keluar, mengakhiri loop menu.")
                break 
            }
            log.Printf("PUBLISHER: Error membaca input menu: %v\n", errRead)
            continue
        }
        
        if !keepRunning {
            break
        }
        
        choiceStr = strings.TrimSpace(choiceStr)
        if choiceStr == "" {
            continue
        }
        choice, errConv := strconv.Atoi(choiceStr)
        if errConv != nil {
            fmt.Println("Input tidak valid, coba lagi.")
            continue
        }
        
        switch choice {
        case 1:
            taskDetail, subInputErr := readInput(mainReader, "Masukkan detail task untuk request: ")
            if subInputErr != nil || !keepRunning {
                if subInputErr != nil && subInputErr != io.EOF {
                    log.Printf("PUBLISHER: Error membaca detail task: %v", subInputErr)
                }
                break 
            }
            if !keepRunning { break }
            correlationID := uuid.NewString()
            reqPayload := RequestPayload{
                Task:           taskDetail,
                ResponseTopic:  responseTopic,
                CorrelationID:  correlationID,
                Timestamp:      time.Now(),
                QoSUsed:        defaultRequestQoS,
                ExpiryInterval: uint32(messageExpiryInterval),
            }
            log.Printf("PUBLISHER: Mengirim request '%s' dengan QoS %d, Expiry %d detik, CID %s...", 
                taskDetail, defaultRequestQoS, messageExpiryInterval, correlationID)
            publishMessage(client, requestTopic, &reqPayload, defaultRequestQoS, false, uint32(messageExpiryInterval))
            
        case 2:
            qosStr, subInputErr := readInput(mainReader, 
                fmt.Sprintf("Masukkan QoS baru (0, 1, atau 2) (sebelumnya: %d): ", defaultRequestQoS))
            if subInputErr != nil || !keepRunning {
                if subInputErr != nil && subInputErr != io.EOF {
                    log.Printf("PUBLISHER: Error membaca input QoS: %v", subInputErr)
                }
                break 
            }
            if !keepRunning { break }
            qosInt, errQoS := strconv.Atoi(qosStr)
            if errQoS == nil && (qosInt >= 0 && qosInt <= 2) {
                defaultRequestQoS = byte(qosInt)
                log.Printf("PUBLISHER: QoS default untuk request diubah menjadi %d", defaultRequestQoS)
            } else {
                fmt.Println("Input QoS tidak valid.")
            }
            
        case 3:
            statusMsg, subInputErr := readInput(mainReader, 
                "Masukkan status baru untuk retained message (misal: 'sibuk', 'perbaikan'): ")
            if subInputErr != nil || !keepRunning {
                if subInputErr != nil && subInputErr != io.EOF {
                    log.Printf("PUBLISHER: Error membaca input status: %v", subInputErr)
                }
                break
            }
            if !keepRunning { break }
            statusPayload := StatusPayload{Status: statusMsg, Timestamp: time.Now(), ClientID: clientID}
            publishRetainedMessage(client, requesterStatusTopic, statusPayload, 1, true)
            
        case 4:
            log.Println("PUBLISHER: Menghapus retained message dari topik status...")
            // For retained message deletion, use the standard publish method
            token := client.Publish(requesterStatusTopic, 0, true, []byte{})
            token.Wait()
            log.Println("PUBLISHER: Perintah hapus retained message terkirim.")
            
        case 5:
            log.Println("PUBLISHER: Diam selama 25 detik. Keep-Alive akan bekerja (PING/PONG akan terlihat dalam log)...")
            timeout := time.After(25 * time.Second)
            interruptedInIdle := false
            for !interruptedInIdle && keepRunning {
                select {
                case <-timeout:
                    log.Println("PUBLISHER: Selesai periode diam.")
                    interruptedInIdle = true
                case <-time.After(200 * time.Millisecond): 
                }
            }
            
        case 6:
            log.Printf("PUBLISHER: Statistik Ping-Pong:")
            log.Printf("  - PING dikirim: %d", atomic.LoadUint64(&pingCount))
            log.Printf("  - PONG diterima: %d", atomic.LoadUint64(&pongCount))
            if lastPingTime.IsZero() {
                log.Printf("  - Belum ada PING yang dikirim")
            } else {
                log.Printf("  - PING terakhir dikirim: %v", lastPingTime.Format(time.RFC3339))
            }
            
        case 7:
            expiryStr, subInputErr := readInput(mainReader, 
                fmt.Sprintf("Masukkan Message Expiry Interval baru dalam detik (sebelumnya: %d): ", 
                    messageExpiryInterval))
            if subInputErr != nil || !keepRunning {
                if subInputErr != nil && subInputErr != io.EOF {
                    log.Printf("PUBLISHER: Error membaca input expiry: %v", subInputErr)
                }
                break
            }
            if !keepRunning { break }
            expiryInt, errExpiry := strconv.Atoi(expiryStr)
            if errExpiry == nil && expiryInt >= 0 {
                messageExpiryInterval = expiryInt
                log.Printf("PUBLISHER: Message Expiry Interval diubah menjadi %d detik", messageExpiryInterval)
            } else {
                fmt.Println("Input Expiry Interval tidak valid.")
            }
            
        case 8:
            // Send manual ping to test connection
            log.Println("PUBLISHER: Mengirim PING manual ke broker...")
            if client.IsConnected() {
                sendManualPing(client)
            } else {
                log.Println("PUBLISHER: Koneksi tertutup, tidak dapat mengirim PING")
            }
            
        case 0:
            log.Println("PUBLISHER: Keluar dari menu...")
            keepRunning = false
            
        default:
            fmt.Println("Pilihan tidak dikenal.")
        }
        
        if !keepRunning {
            break
        }
    }

    log.Println("PUBLISHER: Publish status 'offline' retained sebelum keluar...")
    publishRetainedMessage(client, requesterStatusTopic, 
        StatusPayload{Status: "offline", Timestamp: time.Now(), ClientID: clientID}, 1, true)
    
    time.Sleep(200 * time.Millisecond) 

    log.Println("PUBLISHER: Unsubscribe dan disconnect...")
    if token := client.Unsubscribe(responseTopic); token.WaitTimeout(3*time.Second) && token.Error() != nil {
        log.Printf("PUBLISHER: Gagal unsubscribe dari '%s': %v", responseTopic, token.Error())
    }
    client.Disconnect(500)

    log.Println("PUBLISHER: Keluar dengan bersih.")
}

func init() {
    rand.New(rand.NewSource(time.Now().UnixNano()))
    lastPingTime = time.Time{} // Initialize as zero time
}