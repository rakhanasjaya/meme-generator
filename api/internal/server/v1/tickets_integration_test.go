package v1

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nyxsr/meme-portal/api/internal/config"
	"github.com/nyxsr/meme-portal/api/internal/platform/storage"
	"github.com/nyxsr/meme-portal/api/internal/platform/token"
)

func TestMemeQuotaAndCheckoutIntegration(t *testing.T) {
	dsn := os.Getenv("MEME_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MEME_TEST_DATABASE_URL to run PostgreSQL integration checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "meme_test_" + token.RandomHex(8)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL)")
	for _, name := range []string{"000001_initialize-extension", "000002_core_schema", "000003_premium_plans", "000004_idempotency_key", "000005_plan_quotas", "000006_meme_soft_delete"} {
		data, err := os.ReadFile(filepath.Join("../../../migrations", name+".up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		exec(string(data))
	}

	user := func(email string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, "INSERT INTO users (email, password_hash) VALUES ($1, '') RETURNING id", email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := user("owner@example.invalid")
	buyer := user("buyer@example.invalid")
	var plan string
	if err := pool.QueryRow(ctx, "SELECT id FROM plans WHERE active AND max_memes > 0 LIMIT 1").Scan(&plan); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE plans SET max_memes = 1 WHERE id = $1", plan)
	exec("INSERT INTO subscriptions (user_id, plan_id, status, starts_at, ends_at) VALUES ($1, $2, 'active', now(), now() + interval '1 hour')", owner, plan)

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("storage method = %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()
	storageConfig := config.StorageConfig{
		Endpoint: strings.TrimPrefix(s3Server.URL, "http://"), Region: "test",
		AccessKey: "test", SecretKey: "test", Bucket: "test",
		PublicBaseURL: "https://storage.example.invalid",
	}
	client, err := storage.NewStorage(ctx, storageConfig)
	if err != nil {
		t.Fatal(err)
	}
	tokens := token.NewManager("test-secret", "test", time.Hour, time.Hour, nil)
	mux := http.NewServeMux()
	Mount(mux, pool, nil, client, tokens, config.Config{Storage: storageConfig})
	pair := func(userID string) string {
		t.Helper()
		p, err := tokens.Issue(ctx, userID, "user")
		if err != nil {
			t.Fatal(err)
		}
		return p.AccessToken
	}
	ownerToken, buyerToken := pair(owner), pair(buyer)
	request := func(method, path, access, key string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	upload := "/api/v1/memes/upload?title=Test&filename=test.png"
	if w := request("POST", upload, ownerToken, "", []byte("text fixture")); w.Code != 400 {
		t.Fatalf("invalid image: %d %s", w.Code, w.Body)
	}
	parallel := func(send func(int) *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
		results := make([]*httptest.ResponseRecorder, 6)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = send(i)
			}()
		}
		wg.Wait()
		return results
	}
	created := 0
	for _, w := range parallel(func(_ int) *httptest.ResponseRecorder {
		return request("POST", upload, ownerToken, "", imageData.Bytes())
	}) {
		switch w.Code {
		case http.StatusCreated:
			created++
		case http.StatusForbidden:
		default:
			t.Fatalf("upload: %d %s", w.Code, w.Body)
		}
	}
	if created != 1 {
		t.Fatalf("quota admits %d uploads, want 1", created)
	}
	var id string
	if err := pool.QueryRow(ctx, "SELECT id FROM memes WHERE user_id = $1", owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	memePath := "/api/v1/memes/" + id
	if w := request("POST", memePath+"/result-upload", ownerToken, "", imageData.Bytes()); w.Code != 200 {
		t.Fatalf("last reserved meme cannot upload result: %d %s", w.Code, w.Body)
	}
	if w := request("POST", memePath+"/complete", ownerToken, "", []byte("{\"resultUrl\":\"https://storage.example.invalid/result/"+id+".png\"}")); w.Code != 200 {
		t.Fatalf("last reserved meme cannot complete: %d %s", w.Code, w.Body)
	}
	if w := request("DELETE", memePath, buyerToken, "", nil); w.Code != 404 {
		t.Fatalf("ownership check: status %d", w.Code)
	}
	if w := request("DELETE", memePath, ownerToken, "", nil); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := request("POST", upload, ownerToken, "", imageData.Bytes()); w.Code != 403 {
		t.Fatalf("soft delete released quota: %d %s", w.Code, w.Body)
	}
	exec("UPDATE subscriptions SET ends_at = now() WHERE user_id = $1", owner)
	if w := request("GET", "/api/v1/subscription/status", ownerToken, "", nil); w.Code != 200 ||
		w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), "\"subscribed\":false") {
		t.Fatalf("expired status: %d %s %s", w.Code, w.Header(), w.Body)
	}
	var code string
	if err := pool.QueryRow(ctx, "SELECT code FROM plans WHERE id = $1", plan).Scan(&code); err != nil {
		t.Fatal(err)
	}
	for _, w := range parallel(func(i int) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/subscription/checkout", buyerToken, token.RandomHex(8), []byte("{\"planCode\":\""+code+"\"}"))
	}) {
		if w.Code != 201 {
			t.Fatalf("checkout: %d %s", w.Code, w.Body)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM subscriptions WHERE user_id = $1", buyer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending checkouts = %d, want 1 (error: %v)", count, err)
	}
}
