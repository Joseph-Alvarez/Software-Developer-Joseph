// indexer.go OPTIMIZACION
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
	batchSize      = 500
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

// Crear Indice
func createIndex() error {
	req, err := http.NewRequest("GET", zincURL+"/api/index/"+indexName, nil)
	if err != nil {
		return fmt.Errorf("error al crear request: %v", err)
	}

	req.SetBasicAuth(username, password)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error al verificar índice: %v", err)
	}

	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		fmt.Println("Índice ya existe, saltando creación")
		return nil
	}
	resp.Body.Close()

	// Crear el Indice
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

// funcion indexacion paralela
func indexWorker(id int, jobs <-chan []Email, results chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	for batch := range jobs {
		err := indexEmailBatch(batch, 0)
		results <- err

		for i := range batch {
			batch[i] = Email{}
		}
	}
}

// Optimizador Dinamico de Nucleos
func optimizeWorkerCount() int {
	cpus := runtime.NumCPU()
	if cpus <= 4 {
		return cpus
	} else if cpus <= 8 {
		return cpus - 1
	} else {
		return cpus - 2
	}
}

// Procesar correos en Lotes
func processEmailBatches(emailsChan <-chan Email) error {
	batch := make([]Email, 0, batchSize)
	totalProcessed := 0
	maxWorkers := optimizeWorkerCount()

	jobs := make(chan []Email, maxWorkers*2)
	results := make(chan error, maxWorkers*2)
	var wg sync.WaitGroup

	for w := 1; w <= maxWorkers; w++ {
		wg.Add(1)
		go indexWorker(w, jobs, results, &wg)
	}

	//Resultados del proceso
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

	//Procesar correos electrónicos y crear lotes
	for email := range emailsChan {
		batch = append(batch, email)

		if len(batch) >= batchSize {
			jobs <- append([]Email{}, batch...)

			batch = make([]Email, 0, batchSize)

			totalProcessed += batchSize
			if totalProcessed%reportInterval == 0 {
				fmt.Printf("Indexados aproximadamente %d documentos...\n", totalProcessed)
			}
		}
	}

	//Procesar los correos electrónicos restantes
	if len(batch) > 0 {
		jobs <- append([]Email{}, batch...)
		totalProcessed += len(batch)
	}

	close(jobs)
	wg.Wait()
	close(results)

	resultsWg.Wait()

	//Comprobar si hay errores
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

// Optimizado de archivos JSON con streaming
func readJSONFile(path string) (Email, error) {
	var email Email

	file, err := os.Open(path)
	if err != nil {
		return email, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, fileBufferSize)
	decoder := json.NewDecoder(reader)

	if err := decoder.Decode(&email); err != nil {
		return email, err
	}

	//Normalizar la fecha si es Necesario
	if email.Date != "" {
		parsedDate, err := time.Parse(time.RFC1123Z, email.Date)
		if err == nil {
			email.Date = parsedDate.Format(time.RFC3339)
		}
	}

	return email, nil
}

// Procesar archivos en fragmentos
func processFiles(files []string) error {
	numWorkers := optimizeWorkerCount()
	fmt.Printf("Procesando archivos con %d workers\n", numWorkers)

	//correos electrónicos procesados
	emailsChan := make(chan Email, batchSize)

	// Error canal
	errorsChan := make(chan error, numWorkers)

	//Iniciar Proceso
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(emailsChan)

		//Procesar Archivos en Fragmentos
		chunkSize := 1000
		for i := 0; i < len(files); i += chunkSize {
			end := i + chunkSize
			if end > len(files) {
				end = len(files)
			}

			chunk := files[i:end]
			filesChan := make(chan string, len(chunk))
			var chunkWg sync.WaitGroup

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
