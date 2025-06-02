package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io" // Diperlukan untuk io.EOF
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

const (
	broker             = "ssl://localhost:8883"
	requestTopic       = "app/request/job"
	requesterStatusTopic = "app/status/requester"
	clientID           = "go-mqtt-interactive-pub-fix2" // Client ID diperbarui lagi
	caCertFile         = "../certs/ca.crt"
	username           = "testuser"
	password           = "testuser" // GANTI DENGAN PASSWORD YANG BENAR
)

var responseTopic = fmt.Sprintf("app/response/job/%s", clientID)
var defaultRequestQoS byte = 1

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
	log.Println("PUBLISHER: Terautentikasi & terhubung dengan aman (MQTTS v5, Interaktif)")
	if token := client.Subscribe(responseTopic, 1, responseMessageHandler); token.Wait() && token.Error() != nil {
		log.Printf("PUBLISHER: Gagal subscribe ke response topic '%s': %v", responseTopic, token.Error())
	} else {
		log.Printf("PUBLISHER: Berhasil subscribe ke response topic: %s", responseTopic)
	}
	publishRetainedMessage(client, requesterStatusTopic, StatusPayload{Status: "online", Timestamp: time.Now(), ClientID: clientID}, 1, true)
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
	log.Printf("PUBLISHER: Terima respons (QoS: %d) untuk CID [%s]: '%s'", msg.Qos(), respPayload.CorrelationID, respPayload.Result)
}

// Fungsi utilitas untuk publish pesan generik
func publishMessage(client mqtt.Client, topic string, payload interface{}, qos byte, retained bool) {
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
			log.Printf("PUBLISHER: Berhasil publish ke topik %s (QoS: %d, Retained: %t)", topic, qos, retained)
		}
	} else {
		log.Printf("PUBLISHER: Timeout saat publish ke topik %s", topic)
	}
}

// Wrapper spesifik untuk retained status
func publishRetainedMessage(client mqtt.Client, topic string, statusPayload StatusPayload, qos byte, retained bool) {
	log.Printf("PUBLISHER: Mempublish status '%s' ke topik '%s' (Retained: %t, QoS: %d)", statusPayload.Status, topic, retained, qos)
	publishMessage(client, topic, statusPayload, qos, retained)
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

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
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
	opts.SetProtocolVersion(5)
	opts.SetUsername(username)
	opts.SetPassword(password)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetKeepAlive(10 * time.Second)

	client := mqtt.NewClient(opts)
	log.Printf("PUBLISHER: Mencoba menghubungkan dengan username '%s'...", username)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(fmt.Errorf("PUBLISHER: Gagal connect ke broker MQTTS: %w", token.Error()))
	}

	mainReader := bufio.NewReader(os.Stdin)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	keepRunning := true

	go func() {
		<-sigChan
		log.Println("\nPUBLISHER: Sinyal interrupt diterima, memproses keluar...")
		keepRunning = false
	}()

	for keepRunning {
		fmt.Printf("\n--- Pilihan Aksi Publisher ---\n")
		fmt.Printf("1. Kirim Request (QoS saat ini: %d)\n", defaultRequestQoS)
		fmt.Println("2. Ubah QoS Default untuk Request Berikutnya")
		fmt.Println("3. Publish Retained Status (misal: 'sibuk')")
		fmt.Println("4. Hapus Retained Status (kirim payload kosong)")
		fmt.Println("5. Diam selama 25 detik (Keep-Alive=10 detik akan aktif di background)")
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
		if choiceStr == "" { // Jika input kosong (misalnya hanya Enter), ulangi menu
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
			if !keepRunning { break } // Periksa setelah input
			correlationID := uuid.NewString()
			reqPayload := RequestPayload{
				Task:          taskDetail,
				ResponseTopic: responseTopic,
				CorrelationID: correlationID,
				Timestamp:     time.Now(),
				QoSUsed:       defaultRequestQoS,
			}
			log.Printf("PUBLISHER: Mengirim request '%s' dengan QoS %d, CID %s...", taskDetail, defaultRequestQoS, correlationID)
			publishMessage(client, requestTopic, reqPayload, defaultRequestQoS, false)
		case 2:
			qosStr, subInputErr := readInput(mainReader, fmt.Sprintf("Masukkan QoS baru (0, 1, atau 2) (sebelumnya: %d): ", defaultRequestQoS))
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
			statusMsg, subInputErr := readInput(mainReader, "Masukkan status baru untuk retained message (misal: 'sibuk', 'perbaikan'): ")
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
			token := client.Publish(requesterStatusTopic, 0, true, []byte{})
			token.Wait()
			log.Println("PUBLISHER: Perintah hapus retained message terkirim.")
		case 5:
			log.Println("PUBLISHER: Diam selama 25 detik. Keep-Alive (10 detik) akan bekerja (tanpa log PING eksplisit jika DEBUG Paho mati)...")
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
	publishRetainedMessage(client, requesterStatusTopic, StatusPayload{Status: "offline", Timestamp: time.Now(), ClientID: clientID}, 1, true)
	
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
}