package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

var redisClient *redis.Client

func init() {
	redisAddr := os.Getenv("REDIS_URL")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
}

type CreateSecretRequest struct {
	Secret     string `json:"secret"`
	Type       string `json:"type"`
	ExpiryTime int    `json:"expiry_time"`
	MaxViews   int    `json:"max_views"`
}

type CreateSecretResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxViews  int       `json:"max_views"`
}

type ViewSecretResponse struct {
	Secret    string `json:"secret"`
	Type      string `json:"type"`
	ViewCount int    `json:"view_count"`
	MaxViews  int    `json:"max_views"`
	ExpiresAt string `json:"expires_at"`
}

type HealthResponse struct {
	Status        string    `json:"status"`
	Timestamp     time.Time `json:"timestamp"`
	SecretsStored int64     `json:"secrets_stored"`
}

var encryptionKey = make([]byte, 32)

func init() {
	if _, err := rand.Read(encryptionKey); err != nil {
		log.Fatal("Failed to generate encryption key:", err)
	}
}

func encryptSecret(plaintext string) (string, error) {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func decryptSecret(ciphertext string) (string, error) {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	ct, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	nonceSize := aead.NonceSize()
	if len(ct) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ct := ct[:nonceSize], ct[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func generateSecretID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "secret_" + hex.EncodeToString(b), nil
}

func CreateSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Secret == "" || req.ExpiryTime <= 0 || req.MaxViews <= 0 {
		http.Error(w, "Invalid parameters", http.StatusBadRequest)
		return
	}

	encrypted, err := encryptSecret(req.Secret)
	if err != nil {
		http.Error(w, "Encryption failed", http.StatusInternalServerError)
		return
	}

	id, err := generateSecretID()
	if err != nil {
		http.Error(w, "ID generation failed", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	expiresAt := time.Now().Add(time.Duration(req.ExpiryTime) * time.Second)
	
	data := map[string]interface{}{
		"encrypted": encrypted,
		"type":      req.Type,
		"max_views": req.MaxViews,
		"view_count": 0,
		"expires_at": expiresAt.Unix(),
	}

	jsonData, _ := json.Marshal(data)
	redisClient.Set(ctx, id, string(jsonData), time.Duration(req.ExpiryTime)*time.Second)

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	response := CreateSecretResponse{
		ID:        id,
		URL:       fmt.Sprintf("%s/secret/%s", baseURL, id),
		ExpiresAt: expiresAt,
		MaxViews:  req.MaxViews,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func CheckSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	val, err := redisClient.Get(ctx, req.ID).Result()
	if err == redis.Nil {
		http.Error(w, "Secret not found", http.StatusNotFound)
		return
	}

	var data map[string]interface{}
	json.Unmarshal([]byte(val), &data)

	viewCount := int(data["view_count"].(float64))
	maxViews := int(data["max_views"].(float64))

	// Just return metadata, don't increment view count
	response := map[string]interface{}{
		"type":       data["type"].(string),
		"view_count": viewCount,
		"max_views":  maxViews,
		"expires_at": time.Unix(int64(data["expires_at"].(float64)), 0).Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func ViewSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.ID == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	val, err := redisClient.Get(ctx, req.ID).Result()
	if err == redis.Nil {
		http.Error(w, "Secret not found", http.StatusNotFound)
		return
	}

	var data map[string]interface{}
	json.Unmarshal([]byte(val), &data)

	viewCount := int(data["view_count"].(float64))
	maxViews := int(data["max_views"].(float64))

	if viewCount >= maxViews {
		redisClient.Del(ctx, req.ID)
		http.Error(w, "Maximum views reached", http.StatusGone)
		return
	}

	encrypted := data["encrypted"].(string)
	plaintext, err := decryptSecret(encrypted)
	if err != nil {
		http.Error(w, "Decryption failed", http.StatusInternalServerError)
		return
	}

	viewCount++
	data["view_count"] = viewCount

	if viewCount >= maxViews {
		redisClient.Del(ctx, req.ID)
	} else {
		jsonData, _ := json.Marshal(data)
		ttl := time.Unix(int64(data["expires_at"].(float64)), 0).Sub(time.Now())
		redisClient.Set(ctx, req.ID, string(jsonData), ttl)
	}

	response := ViewSecretResponse{
		Secret:    plaintext,
		Type:      data["type"].(string),
		ViewCount: viewCount,
		MaxViews:  maxViews,
		ExpiresAt: time.Unix(int64(data["expires_at"].(float64)), 0).Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func BurnSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	deleted := redisClient.Del(ctx, req.ID)

	if deleted.Val() == 0 {
		http.Error(w, "Secret not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"message": "Secret burned successfully",
	})
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, _ := redisClient.DBSize(ctx).Result()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:        "ok",
		Timestamp:     time.Now(),
		SecretsStored: count,
	})
}

func ServeHomepage(w http.ResponseWriter, r *http.Request) {
	html, err := ioutil.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

func ServeSecretPage(w http.ResponseWriter, r *http.Request) {
	html, err := ioutil.ReadFile("secrets.html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

func ServeSecretViewPage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	secretID := vars["id"]
	
	html, err := ioutil.ReadFile("secret_view.html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	
	// Replace placeholder with actual secret ID
	htmlStr := strings.ReplaceAll(string(html), "const secretID = 'PLACEHOLDER_ID'", fmt.Sprintf("const secretID = '%s'", secretID))
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlStr))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	router := mux.NewRouter()

	router.HandleFunc("/", ServeHomepage).Methods("GET")
	router.HandleFunc("/secrets", ServeSecretPage).Methods("GET")
	router.HandleFunc("/secret/{id}", ServeSecretViewPage).Methods("GET")

	router.HandleFunc("/api/health", HealthCheck).Methods("GET")
	router.HandleFunc("/api/create", CreateSecret).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/check", CheckSecret).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/view", ViewSecret).Methods("POST", "OPTIONS")
	router.HandleFunc("/api/burn", BurnSecret).Methods("POST", "OPTIONS")

	headersOk := handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type"})
	originsOk := handlers.AllowedOrigins([]string{"*"})
	methodsOk := handlers.AllowedMethods([]string{"GET", "HEAD", "POST", "PUT", "OPTIONS"})

	log.Printf("Greentropik Secrets API starting on port %s", port)
	handler := handlers.CORS(originsOk, headersOk, methodsOk)(router)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}