package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"
)

// Email estructura para los correos
//
//go:generate easyjson -all main.go
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
	zincURL        = "http://localhost:4080"
	indexName      = "enron_emails_Opti"
	batchSize      = 500 // Increased batch size for better efficiency
	username       = "admin"
	password       = "Complexpass#123"
	jsonDir        = "C:/Users/Joseph Ordoñez/Desktop/Proyecto_GO/output_json"
	maxRetries     = 3
	retryTimeout   = 5 * time.Second
	pprofPort      = ":6060"
	fileBufferSize = 65536 // 64KB buffer for file reading
	contentMaxLen  = 10000 // Maximum content length to index
	reportInterval = 5000  // Report progress every X documents
)

// Reusable HTTP client for all requests
var httpClient = &http.Client{
	Timeout: time.Second * 60,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

// Optimized pools for resource reuse
var (
	// Pool of bytes.Buffer for reuse
	bufferPool = sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}

	// Pool of Email structs for reuse
	emailPool = sync.Pool{
		New: func() interface{} {
			return new(Email)
		},
	}
)

func createIndex() error {
	// Check if index already exists
	req, err := http.NewRequest("GET", zincURL+"/api/index/"+indexName, nil)
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error al verificar índice: %v", err)
	}

	// If index already exists, return
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		fmt.Println("Índice ya existe, saltando creación")
		return nil
	}
	resp.Body.Close()

	// Create the index
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

	buffer := bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	defer bufferPool.Put(buffer)

	if err := json.NewEncoder(buffer).Encode(mapping); err != nil {
		return fmt.Errorf("error al serializar mapping: %v", err)
	}

	req, err = http.NewRequest("POST", zincURL+"/api/index", buffer)
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "application/json")

	resp, err = httpClient.Do(req)
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
	if len(batch) == 0 {
		return nil
	}

	buffer := bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	defer bufferPool.Put(buffer)

	for _, email := range batch {
		// Truncate large content
		if len(email.Content) > contentMaxLen {
			email.Content = email.Content[:contentMaxLen] + "... (content truncated)"
		}

		op := BulkOperation{}
		op.Index.Index = indexName
		if err := json.NewEncoder(buffer).Encode(op); err != nil {
			return fmt.Errorf("error al serializar operación: %v", err)
		}

		if err := json.NewEncoder(buffer).Encode(email); err != nil {
			return fmt.Errorf("error al serializar email: %v", err)
		}
	}

	req, err := http.NewRequest("POST", zincURL+"/api/_bulk", buffer)
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := httpClient.Do(req)
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

// Worker function for parallel indexing
func indexWorker(id int, jobs <-chan []Email, results chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	for batch := range jobs {
		err := indexEmailBatch(batch, 0)
		results <- err

		// Return emails to pool
		for i := range batch {
			batch[i] = Email{} // Clear fields
		}
	}
}

// Optimized dynamic worker count
func optimizeWorkerCount() int {
	cpus := runtime.NumCPU()
	// Use more workers for I/O bound operations
	// but avoid overloading the system
	if cpus <= 4 {
		return cpus
	} else if cpus <= 8 {
		return cpus - 1
	} else {
		return cpus - 2
	}
}

// Process emails in batches
func processEmailBatches(emailsChan <-chan Email) error {
	batch := make([]Email, 0, batchSize)
	totalProcessed := 0
	maxWorkers := optimizeWorkerCount()

	// Create worker pool
	jobs := make(chan []Email, maxWorkers*2)
	results := make(chan error, maxWorkers*2)
	var wg sync.WaitGroup

	// Start workers
	for w := 1; w <= maxWorkers; w++ {
		wg.Add(1)
		go indexWorker(w, jobs, results, &wg)
	}

	// Process results in a separate goroutine
	errChan := make(chan error, 1)
	var resultsWg sync.WaitGroup
	resultsWg.Add(1)

	go func() {
		defer resultsWg.Done()
		failedBatches := 0

		for err := range results {
			if err != nil {
				log.Printf("Error al indexar batch: %v", err)
				failedBatches++
				if failedBatches > 10 {
					errChan <- fmt.Errorf("demasiados errores consecutivos, abortando")
					return
				}
			}
		}
		errChan <- nil
	}()

	// Process emails and create batches
	for email := range emailsChan {
		batch = append(batch, email)

		if len(batch) >= batchSize {
			// Send full batch to worker
			jobs <- append([]Email{}, batch...) // Make a copy to avoid data races

			// Reset batch
			batch = make([]Email, 0, batchSize)

			totalProcessed += batchSize
			if totalProcessed%reportInterval == 0 {
				fmt.Printf("Indexados aproximadamente %d documentos...\n", totalProcessed)
			}
		}
	}

	// Process any remaining emails
	if len(batch) > 0 {
		jobs <- append([]Email{}, batch...)
		totalProcessed += len(batch)
	}

	// Close channels and wait for workers to finish
	close(jobs)
	wg.Wait()
	close(results)

	// Wait for results processing
	resultsWg.Wait()

	// Check for errors
	select {
	case err := <-errChan:
		if err != nil {
			return err
		}
	default:
	}

	fmt.Printf("Indexación completa. Total de documentos indexados: %d\n", totalProcessed)
	return nil
}

// Optimized JSON file processing with streaming
func readJSONFile(path string) (Email, error) {
	var email Email

	file, err := os.Open(path)
	if err != nil {
		return email, err
	}
	defer file.Close()

	// Use larger buffer for better performance
	reader := bufio.NewReaderSize(file, fileBufferSize)
	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&email); err != nil {
		return email, err
	}

	// Parse and normalize date if needed
	if email.Date != "" {
		parsedDate, err := time.Parse(time.RFC1123Z, email.Date)
		if err == nil {
			email.Date = parsedDate.Format(time.RFC3339)
		}
	}

	return email, nil
}

// Process files in chunks to reduce memory usage
func processFiles(files []string) error {
	numWorkers := optimizeWorkerCount()
	fmt.Printf("Procesando archivos con %d workers\n", numWorkers)

	// Channel for processed emails
	emailsChan := make(chan Email, batchSize)

	// Error channel
	errorsChan := make(chan error, numWorkers)

	// Start file processing goroutine
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(emailsChan)

		// Process files in chunks
		chunkSize := 1000
		for i := 0; i < len(files); i += chunkSize {
			end := i + chunkSize
			if end > len(files) {
				end = len(files)
			}

			chunk := files[i:end]
			filesChan := make(chan string, len(chunk))
			var chunkWg sync.WaitGroup

			// Start workers for this chunk
			for w := 0; w < numWorkers; w++ {
				chunkWg.Add(1)
				go func() {
					defer chunkWg.Done()
					for path := range filesChan {
						email, err := readJSONFile(path)
						if err != nil {
							select {
							case errorsChan <- fmt.Errorf("error procesando %s: %v", path, err):
							default:
							}
							continue
						}
						emailsChan <- email
					}
				}()
			}

			// Feed files to workers
			for _, path := range chunk {
				filesChan <- path
			}
			close(filesChan)

			// Wait for this chunk to finish
			chunkWg.Wait()

			fmt.Printf("Procesados %d/%d archivos...\n", end, len(files))

			// Trigger GC after each chunk to free memory
			runtime.GC()
		}
	}()

	// Start error collector
	go func() {
		for err := range errorsChan {
			log.Printf("Warning: %v", err)
		}
	}()

	// Process emails for indexing
	err := processEmailBatches(emailsChan)

	// Wait for file processing to complete
	wg.Wait()
	close(errorsChan)

	return err
}

func startPprofServer() {
	mux := http.NewServeMux()
	// Register pprof handlers explicitly
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
	// Configure GOMAXPROCS
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)
	fmt.Printf("Usando %d CPUs (GOMAXPROCS)\n", numCPU)

	// Start pprof server in a goroutine
	go startPprofServer()

	// Wait a moment for the server to start
	time.Sleep(time.Second)

	// Create file for CPU profile
	cpuProfile, err := os.Create("cpu.prof")
	if err != nil {
		log.Fatal(err)
	}
	pprof.StartCPUProfile(cpuProfile)
	defer pprof.StopCPUProfile()

	// Create file for Memory profile
	memProfile, err := os.Create("mem.prof")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		pprof.WriteHeapProfile(memProfile)
		memProfile.Close()
	}()

	startTime := time.Now()

	fmt.Println("Creando índice en ZincSearch...")
	if err := createIndex(); err != nil {
		log.Fatalf("Error al crear índice: %v", err)
	}
	fmt.Println("Índice verificado/creado exitosamente")

	fmt.Println("Obteniendo lista de archivos JSON...")
	var files []string
	err = filepath.Walk(jsonDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Error al obtener lista de archivos: %v", err)
	}
	fmt.Printf("Encontrados %d archivos JSON\n", len(files))

	fmt.Println("Comenzando procesamiento de archivos...")
	if err := processFiles(files); err != nil {
		log.Fatalf("Error al procesar archivos: %v", err)
	}

	fmt.Printf("Tiempo total de ejecución: %v\n", time.Since(startTime))

	// Print memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("Uso de memoria: Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB\n",
		m.Alloc/1024/1024, m.TotalAlloc/1024/1024, m.Sys/1024/1024)
}
