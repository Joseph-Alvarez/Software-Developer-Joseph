// backend/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Estructura para la respuesta de ZincSearch
type SearchResponse struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	Hits     struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []struct {
			Index  string                 `json:"_index"`
			ID     string                 `json:"_id"`
			Score  float64                `json:"_score"`
			Source map[string]interface{} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// Configuración de ZincSearch
type ZincSearchConfig struct {
	Host     string
	Username string
	Password string
	Index    string
}

// Configuración de AWS con LocalStack
var (
	s3Client   *s3.S3
	bucketName = "zincsearch-backup"
	config     ZincSearchConfig
)

func init() {
	// Configuración de ZincSearch
	config = ZincSearchConfig{
		Host:     "http://localhost:4080", // Cambia esto si usas otro puerto
		Username: "admin",
		Password: "Complexpass#123",
		Index:    "enron_emails_Opti",
	}

	// Cargar variables de entorno si existen
	if host := os.Getenv("ZINC_HOST"); host != "" {
		config.Host = host
	}
	if user := os.Getenv("ZINC_USER"); user != "" {
		config.Username = user
	}
	if pass := os.Getenv("ZINC_PASS"); pass != "" {
		config.Password = pass
	}
	if index := os.Getenv("ZINC_INDEX"); index != "" {
		config.Index = index
	}

	// Configurar sesión con LocalStack (S3)
	sess := session.Must(session.NewSession(&aws.Config{
		Region:           aws.String("us-east-1"),
		Endpoint:         aws.String("http://localhost:4566"), // LocalStack
		Credentials:      credentials.NewStaticCredentials("test", "test", ""),
		S3ForcePathStyle: aws.Bool(true), // 👈 IMPORTANTE: Esto evita el error de DNS
	}))

	// Crear cliente S3
	s3Client = s3.New(sess)

	// Verificar si el bucket existe, si no, crearlo
	_, err := s3Client.HeadBucket(&s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err != nil {
		log.Printf("Bucket %s no encontrado. Creándolo...", bucketName)
		_, err := s3Client.CreateBucket(&s3.CreateBucketInput{
			Bucket: aws.String(bucketName),
		})
		if err != nil {
			log.Fatalf("Error al crear bucket: %v", err)
		}
		log.Printf("Bucket %s creado exitosamente.", bucketName)
	}

	log.Printf("ZincSearch config: Host=%s, User=%s, Index=%s",
		config.Host, config.Username, config.Index)
}

func main() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	// Middleware CORS
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	// Definir rutas API
	r.Get("/api/search", searchHandler)
	r.Get("/api/stats", statsHandler)
	r.Get("/api/email/{id}", emailHandler)

	// Servir archivos estáticos
	staticDir := "../frontend"
	fs := http.FileServer(http.Dir(staticDir))
	r.Handle("/*", http.StripPrefix("/", fs))

	// Página de inicio
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
	})

	// Iniciar respaldo automático a S3 cada 5 minutos - CON CORRECCIÓN
	go func() {
		for {
			// Primero creamos el archivo de respaldo
			err := createBackupFile()
			if err != nil {
				log.Printf("Error creando archivo de backup: %v", err)
			} else {
				// Luego lo subimos a S3
				err = uploadToS3("backup.json", "zinc_backup.json")
				if err != nil {
					log.Printf("Error subiendo backup a S3: %v", err)
				} else {
					log.Printf("Backup completado exitosamente")
				}
			}
			time.Sleep(5 * time.Minute)
		}
	}()

	// Iniciar servidor
	port := 3000
	fmt.Printf("Mamuro is running at http://localhost:%d\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), r))
}

// Función para crear el archivo de respaldo
func createBackupFile() error {
	// Realiza una consulta amplia para obtener todos los datos o los datos relevantes
	backupQuery := map[string]interface{}{
		"search_type": "querystring",
		"query":       map[string]interface{}{"term": "*"},
		"size":        1000, // Ajusta según sea necesario
	}

	result, err := executeZincSearch(backupQuery)
	if err != nil {
		return fmt.Errorf("error al obtener datos para backup: %v", err)
	}

	// Guarda los resultados en archivo backup.json
	backupData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("error al serializar datos: %v", err)
	}

	err = ioutil.WriteFile("backup.json", backupData, 0644)
	if err != nil {
		return fmt.Errorf("error al escribir archivo de backup: %v", err)
	}

	log.Printf("Archivo backup.json creado con %d documentos", len(result.Hits.Hits))
	return nil
}

// Función para subir archivos a S3
func uploadToS3(filePath, key string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("error abriendo el archivo: %v", err)
	}
	defer file.Close()

	_, err = s3Client.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   file,
	})
	if err != nil {
		return fmt.Errorf("error subiendo el archivo a S3: %v", err)
	}

	log.Printf("Archivo %s subido exitosamente a S3 como %s", filePath, key)
	return nil
}

// Handler para búsqueda en ZincSearch
func searchHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Search request received with URL: %s", r.URL.String())

	query := r.URL.Query().Get("q")
	if query == "" {
		query = "*"
	}
	log.Printf("Search query: %s", query)

	sizeStr := r.URL.Query().Get("size")
	size := 20 // Tamaño por defecto
	if sizeStr != "" {
		var err error
		size, err = strconv.Atoi(sizeStr)
		if err != nil {
			size = 20
		}
	}

	// Construir la consulta para ZincSearch
	searchQuery := map[string]interface{}{
		"search_type": "querystring",
		"query": map[string]interface{}{
			"term": query,
		},
		"from":        0,
		"size":        size,
		"sort_fields": []string{"-@timestamp"},
	}

	// Log de la consulta enviada a ZincSearch
	queryJSON, _ := json.MarshalIndent(searchQuery, "", "  ")
	log.Printf("Sending query to ZincSearch: %s", string(queryJSON))

	// Ejecutar búsqueda en ZincSearch
	result, err := executeZincSearch(searchQuery)
	if err != nil {
		log.Printf("Search error: %v", err)
		http.Error(w, fmt.Sprintf("Search error: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Search results received. Total hits: %d", result.Hits.Total.Value)
	log.Printf("Number of documents in result: %d", len(result.Hits.Hits))

	// Devolver resultados en JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Handler para obtener un email
func emailHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Email handler funcionando"))
}

// Función para obtener estadísticas
func statsHandler(w http.ResponseWriter, r *http.Request) {
	countQuery := map[string]interface{}{
		"search_type": "querystring",
		"query":       map[string]interface{}{"term": "*"},
		"size":        0,
	}

	result, err := executeZincSearch(countQuery)
	if err != nil {
		http.Error(w, fmt.Sprintf("Stats error: %v", err), http.StatusInternalServerError)
		return
	}

	stats := map[string]interface{}{
		"total_emails": result.Hits.Total.Value,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// Función para ejecutar búsquedas en ZincSearch
func executeZincSearch(query map[string]interface{}) (*SearchResponse, error) {
	jsonQuery, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	url := fmt.Sprintf("%s/api/%s/_search", config.Host, config.Index)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonQuery))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(config.Username, config.Password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zinc search returned error: %s", string(body))
	}

	var result SearchResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}
