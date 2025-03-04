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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// ZincSearchConfig holds the connection details
type ZincSearchConfig struct {
	Host     string
	Username string
	Password string
	Index    string
}

// SearchResponse structure to parse ZincSearch responses
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

// Global config
var config ZincSearchConfig

func init() {
	// Default config
	config = ZincSearchConfig{
		Host:     "http://localhost:4080", // Make sure this is set correctly
		Username: "admin",
		Password: "Complexpass#123",
		Index:    "enron_emails_Opti",
	}

	// Override with environment variables if set
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

	// Debug log - add this line at the end, before the closing brace
	log.Printf("ZincSearch config: Host=%s, User=%s, Index=%s",
		config.Host, config.Username, config.Index)
}

func main() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	// Agregar middleware CORS
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

	// Definir rutas API primero
	r.Get("/api/search", searchHandler)
	r.Get("/api/stats", statsHandler)
	r.Get("/api/email/{id}", emailHandler) // Nueva ruta para obtener un correo individual

	// Corregir la ruta del directorio estático
	staticDir := "../frontend"
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		log.Printf("Warning: Directory %s does not exist", staticDir)
	} else {
		log.Printf("Static directory found at: %s", staticDir)
	}

	// Servir archivos estáticos
	fs := http.FileServer(http.Dir(staticDir))
	r.Handle("/*", http.StripPrefix("/", fs))

	// Agregar un manejador específico para la ruta raíz
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
	})

	// Iniciar servidor
	port := 3000
	fmt.Printf("Mamuro is running at http://localhost:%d\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), r))
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Search request received with URL: %s", r.URL.String())

	query := r.URL.Query().Get("q")
	if query == "" {
		query = "*" // Default to all documents if no query provided
	}
	log.Printf("Search query: %s", query)

	sizeStr := r.URL.Query().Get("size")
	size := 20 // Default size
	if sizeStr != "" {
		var err error
		size, err = strconv.Atoi(sizeStr)
		if err != nil {
			size = 20 // Fall back to default if conversion fails
		}
	}

	// Build the search query for ZincSearch
	searchQuery := map[string]interface{}{
		"search_type": "querystring",
		"query": map[string]interface{}{
			"term": query,
		},
		"from":        0,    // Integer instead of string
		"size":        size, // Integer instead of string
		"sort_fields": []string{"-@timestamp"},
	}

	// Log the query we're sending to ZincSearch
	queryJSON, _ := json.MarshalIndent(searchQuery, "", "  ")
	log.Printf("Sending query to ZincSearch: %s", string(queryJSON))

	// Execute search against ZincSearch
	result, err := executeZincSearch(searchQuery)
	if err != nil {
		log.Printf("Search error: %v", err)
		http.Error(w, fmt.Sprintf("Search error: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Search results received. Total hits: %d", result.Hits.Total.Value)
	log.Printf("Number of documents in result: %d", len(result.Hits.Hits))

	// Return results as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func emailHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "Email ID is required", http.StatusBadRequest)
		return
	}

	log.Printf("Fetching email with ID: %s", id)

	// Build the query to fetch a single document by ID
	query := map[string]interface{}{
		"search_type": "match",
		"query": map[string]interface{}{
			"_id": id,
		},
		"from": 0,
		"size": 1,
	}

	result, err := executeZincSearch(query)
	if err != nil {
		log.Printf("Error fetching email: %v", err)
		http.Error(w, fmt.Sprintf("Error fetching email: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if we found the document
	if len(result.Hits.Hits) == 0 {
		log.Printf("Email with ID %s not found", id)
		http.Error(w, "Email not found", http.StatusNotFound)
		return
	}

	// Return the document
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result.Hits.Hits[0])
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Query for total documents count
	countQuery := map[string]interface{}{
		"search_type": "querystring",
		"query": map[string]interface{}{
			"term": "*",
		},
		"size": "0",
	}

	result, err := executeZincSearch(countQuery)
	if err != nil {
		http.Error(w, fmt.Sprintf("Stats error: %v", err), http.StatusInternalServerError)
		return
	}

	// Return basic stats
	stats := map[string]interface{}{
		"total_emails": result.Hits.Total.Value,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func executeZincSearch(query map[string]interface{}) (*SearchResponse, error) {
	// Convert query to JSON
	jsonQuery, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Prepare request to ZincSearch
	url := fmt.Sprintf("%s/api/%s/_search", config.Host, config.Index)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonQuery))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.SetBasicAuth(config.Username, config.Password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Enron-Email-Visualizer/1.0")

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for successful response
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zinc search returned error: %s", string(body))
	}

	// Log response for debugging
	log.Printf("ZincSearch response: %s", string(body))

	// Parse response
	var result SearchResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}
