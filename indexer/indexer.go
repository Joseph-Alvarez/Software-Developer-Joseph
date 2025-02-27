package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

// Email estructura para los correos
type Email struct {
	MessageID string `json:"message_id"`
	Date      string `json:"date"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Content   string `json:"content"`
	Folder    string `json:"folder"`
}

// BulkOperation represents the operation metadata for each record
type BulkOperation struct {
	Index struct {
		Index string `json:"_index"`
	} `json:"index"`
}

const (
	zincURL      = "http://localhost:4080"
	indexName    = "enron_emails_2"
	batchSize    = 50
	username     = "admin"
	password     = "Complexpass#123"
	jsonDir      = "C:/Users/Joseph Ordoñez/Desktop/Proyecto_GO/prueba_json"
	maxRetries   = 3
	retryTimeout = 5 * time.Second
	pprofPort    = ":6060"
)

func createIndex() error {
	mapping := map[string]interface{}{
		"name":         indexName,
		"storage_type": "disk",
		"shard_num":    1,
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"message_id": map[string]string{"type": "keyword"},
				"date":       map[string]string{"type": "date"},
				"from":       map[string]string{"type": "keyword"},
				"to":         map[string]string{"type": "keyword"},
				"subject":    map[string]string{"type": "text"},
				"content":    map[string]string{"type": "text"},
				"folder":     map[string]string{"type": "keyword"},
			},
		},
	}

	jsonMapping, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("error al serializar mapping: %v", err)
	}

	req, err := http.NewRequest("POST", zincURL+"/api/index", bytes.NewBuffer(jsonMapping))
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Second * 30}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error al crear índice: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("error al crear índice (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func indexEmailBatch(batch []Email, retryCount int) error {
	var bulkData bytes.Buffer

	for _, email := range batch {
		if len(email.Content) > 100000 {
			email.Content = email.Content[:100000] + "... (content truncated)"
		}

		op := BulkOperation{}
		op.Index.Index = indexName
		if err := json.NewEncoder(&bulkData).Encode(op); err != nil {
			return fmt.Errorf("error al serializar operación: %v", err)
		}

		if err := json.NewEncoder(&bulkData).Encode(email); err != nil {
			return fmt.Errorf("error al serializar email: %v", err)
		}
	}

	req, err := http.NewRequest("POST", zincURL+"/api/_bulk", &bulkData)
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Second * 60}
	resp, err := client.Do(req)
	if err != nil {
		if retryCount < maxRetries {
			time.Sleep(retryTimeout)
			return indexEmailBatch(batch, retryCount+1)
		}
		return fmt.Errorf("error al indexar batch después de %d intentos: %v", maxRetries, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if retryCount < maxRetries {
			time.Sleep(retryTimeout)
			return indexEmailBatch(batch, retryCount+1)
		}
		return fmt.Errorf("error al indexar batch (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func indexEmails(emails []Email) error {
	totalIndexed := 0
	failedBatches := 0

	for i := 0; i < len(emails); i += batchSize {
		end := i + batchSize
		if end > len(emails) {
			end = len(emails)
		}

		batch := emails[i:end]
		if err := indexEmailBatch(batch, 0); err != nil {
			log.Printf("Error al indexar batch %d-%d: %v", i, end, err)
			failedBatches++
			if failedBatches > 10 {
				return fmt.Errorf("demasiados errores consecutivos, abortando")
			}
			continue
		}

		failedBatches = 0
		totalIndexed += len(batch)
		fmt.Printf("Indexados %d documentos...\n", totalIndexed)
	}

	return nil
}

func readJSONFiles() ([]Email, error) {
	var allEmails []Email

	err := filepath.Walk(jsonDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Ext(path) == ".json" {
			fmt.Printf("Leyendo archivo: %s\n", path)

			fileData, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("error al leer archivo %s: %v", path, err)
			}

			var email Email
			if err := json.Unmarshal(fileData, &email); err != nil {
				log.Printf("Warning: error al parsear JSON %s: %v", path, err)
				return nil
			}

			if email.Date != "" {
				parsedDate, err := time.Parse(time.RFC1123Z, email.Date)
				if err == nil {
					email.Date = parsedDate.Format(time.RFC3339)
				}
			}

			allEmails = append(allEmails, email)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error al recorrer directorio: %v", err)
	}

	fmt.Printf("Total de emails leídos: %d\n", len(allEmails))
	return allEmails, nil
}

func startPprofServer() {
	mux := http.NewServeMux()
	// Registramos explícitamente los handlers de pprof
	mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)

	server := &http.Server{
		Addr:    pprofPort,
		Handler: mux,
	}

	log.Printf("Iniciando servidor pprof en http://localhost%s/debug/pprof/\n", pprofPort)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("Error al iniciar servidor pprof: %v\n", err)
	}
}

func main() {
	// Iniciamos el servidor pprof en una goroutine
	go startPprofServer()

	// Esperamos un momento para que el servidor inicie
	time.Sleep(time.Second)

	// Creamos archivo para CPU profile
	cpuProfile, err := os.Create("cpu.prof")
	if err != nil {
		log.Fatal(err)
	}
	pprof.StartCPUProfile(cpuProfile)
	defer pprof.StopCPUProfile()

	// Creamos archivo para Memory profile
	memProfile, err := os.Create("mem.prof")
	if err != nil {
		log.Fatal(err)
	}
	defer memProfile.Close()

	fmt.Println("Creando índice en ZincSearch...")
	if err := createIndex(); err != nil {
		log.Fatalf("Error al crear índice: %v", err)
	}
	fmt.Println("Índice creado exitosamente")

	fmt.Println("Leyendo archivos JSON...")
	emails, err := readJSONFiles()
	if err != nil {
		log.Fatalf("Error al leer archivos JSON: %v", err)
	}
	fmt.Printf("Leídos %d correos en total\n", len(emails))

	fmt.Println("Comenzando indexación en ZincSearch...")
	if err := indexEmails(emails); err != nil {
		log.Fatalf("Error al indexar correos: %v", err)
	}
	fmt.Printf("Indexación completada. Total de documentos indexados: %d\n", len(emails))

	// Guardamos el memory profile al final
	if err := pprof.WriteHeapProfile(memProfile); err != nil {
		log.Fatal(err)
	}
}
