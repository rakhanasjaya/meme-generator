package meme

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"

	"github.com/nyxsr/meme-portal/api/internal/config"
	"github.com/nyxsr/meme-portal/api/internal/platform/token"
)

// Gate memeriksa apakah user berhak membuat meme (langganan aktif).
// Diimplementasikan oleh modul subscription lewat structural typing.
type Gate interface {
	Reserve(ctx context.Context, tx pgx.Tx, userID string) error
}

type Service struct {
	repo    *Repository
	s3      *s3.Client
	bucket  string
	baseURL string // public base URL Garage web endpoint
	gate    Gate
}

func NewService(repo *Repository, s3Client *s3.Client, cfg config.StorageConfig, gate Gate) *Service {
	return &Service{repo: repo, s3: s3Client, bucket: cfg.Bucket, baseURL: cfg.PublicBaseURL, gate: gate}
}

// PrepareUpload stores the validated base image and creates its pending meme.
func (s *Service) PrepareUpload(ctx context.Context, userID, title, ext string, data []byte) (UploadResponse, error) {
	ext = strings.ToLower(ext)
	contentType := http.DetectContentType(data)
	if len(data) == 0 || len(data) > maxImageBytes || !allowedExt(ext) || contentTypeForExt(ext) != contentType {
		return UploadResponse{}, ErrInvalidFile
	}
	objectKey := fmt.Sprintf("base/%s%s", token.RandomHex(12), ext)
	var m Meme
	err := pgx.BeginFunc(ctx, s.repo.pool, func(tx pgx.Tx) error {
		if err := s.gate.Reserve(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.storeImage(ctx, objectKey, contentType, data); err != nil {
			return err
		}
		var err error
		m, err = s.repo.Create(ctx, tx, Meme{UserID: userID, Title: title, BaseImageURL: s.publicURL(objectKey)})
		return err
	})
	if err != nil {
		return UploadResponse{}, err
	}
	return UploadResponse{ID: m.ID, BaseURL: m.BaseImageURL}, nil
}

// PrepareUploadPresigned adalah kontrak upload asli: klien mengirim metadata
// (title, filename, contentType, size opsional) dan menerima presigned PUT URL
// untuk mengunggah gambar dasar langsung ke S3. Validasi tetap dilakukan di
// sini: ekstensi dan content type harus gambar yang diizinkan dan saling cocok,
// serta ukuran (bila dikirim) dibatasi dan ikut ditandatangani ke URL.
func (s *Service) PrepareUploadPresigned(ctx context.Context, userID, title, ext, contentType string, size int64) (UploadResponse, error) {
	ext = strings.ToLower(ext)
	want := contentTypeForExt(ext)
	if want == "" {
		return UploadResponse{}, ErrInvalidFile
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "image/jpg" {
		contentType = "image/jpeg"
	}
	if contentType == "" {
		contentType = want
	}
	if contentType != want || size < 0 || size > maxImageBytes {
		return UploadResponse{}, ErrInvalidFile
	}

	objectKey := fmt.Sprintf("base/%s%s", token.RandomHex(12), ext)
	uploadURL, err := s.presignPut(ctx, objectKey, contentType, size)
	if err != nil {
		return UploadResponse{}, err
	}

	var m Meme
	txErr := pgx.BeginFunc(ctx, s.repo.pool, func(tx pgx.Tx) error {
		if reserveErr := s.gate.Reserve(ctx, tx, userID); reserveErr != nil {
			return reserveErr
		}
		created, createErr := s.repo.Create(ctx, tx, Meme{UserID: userID, Title: title, BaseImageURL: s.publicURL(objectKey)})
		if createErr != nil {
			return createErr
		}
		m = created
		return nil
	})
	if txErr != nil {
		return UploadResponse{}, txErr
	}
	return UploadResponse{ID: m.ID, UploadURL: uploadURL, BaseURL: m.BaseImageURL}, nil
}

// ResultUploadPresigned mengembalikan presigned PUT URL untuk gambar hasil
// render (PNG) milik meme <id>.
func (s *Service) ResultUploadPresigned(ctx context.Context, userID, id string) (UploadResponse, error) {
	m, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return UploadResponse{}, err
	}
	key := resultKey(m.ID)
	uploadURL, err := s.presignPut(ctx, key, "image/png", 0)
	if err != nil {
		return UploadResponse{}, err
	}
	return UploadResponse{ID: m.ID, UploadURL: uploadURL, BaseURL: s.publicURL(key)}, nil
}

func (s *Service) presignPut(ctx context.Context, key, contentType string, size int64) (string, error) {
	input := &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: &contentType}
	if size > 0 {
		input.ContentLength = &size
	}
	req, err := s3.NewPresignClient(s.s3).PresignPutObject(ctx, input, s3.WithPresignExpires(maxPresignTTL))
	if err != nil {
		return "", fmt.Errorf("meme service: presign gagal: %w", err)
	}
	return req.URL, nil
}

// verifyStored menolak (dan menghapus) objek yang sudah tersimpan tetapi
// melebihi batas ukuran atau bukan gambar yang diizinkan. Objek yang belum ada
// atau tidak bisa dicek tidak ditolak.
func (s *Service) verifyStored(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	head, err := s.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil
	}
	contentType := strings.ToLower(aws.ToString(head.ContentType))
	if contentLength(head.ContentLength) > maxImageBytes || (contentType != "" && !allowedContentType(contentType)) {
		_, _ = s.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
		return ErrInvalidFile
	}
	return nil
}

// contentLength membaca ContentLength dari SDK; tipenya int64 atau *int64
// tergantung versi, jadi diterima keduanya.
func contentLength[T int64 | *int64](v T) int64 {
	switch x := any(v).(type) {
	case int64:
		return x
	case *int64:
		if x != nil {
			return *x
		}
	}
	return 0
}

// keyFromURL mengembalikan object key dari URL publik yang dibentuk publicURL.
func (s *Service) keyFromURL(u string) string {
	if s.baseURL == "" {
		return u
	}
	return strings.TrimPrefix(u, strings.TrimRight(s.baseURL, "/")+"/")
}

func (s *Service) ResultUpload(ctx context.Context, userID, id string, data []byte) (UploadResponse, error) {
	m, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return UploadResponse{}, err
	}
	if len(data) == 0 || len(data) > maxImageBytes || http.DetectContentType(data) != "image/png" {
		return UploadResponse{}, ErrInvalidFile
	}
	objectKey := resultKey(m.ID)
	if err := s.storeImage(ctx, objectKey, "image/png", data); err != nil {
		return UploadResponse{}, err
	}
	return UploadResponse{ID: m.ID, BaseURL: s.publicURL(objectKey)}, nil
}

func (s *Service) storeImage(ctx context.Context, key, contentType string, data []byte) error {
	size := int64(len(data))
	_, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket, Key: &key, ContentType: &contentType,
		ContentLength: &size, Body: bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("meme service: simpan gambar: %w", err)
	}
	return nil
}

func (s *Service) Complete(ctx context.Context, userID, id, resultURL string) (Meme, error) {
	meme, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return Meme{}, err
	}
	expected := s.publicURL(resultKey(meme.ID))
	if resultURL != expected {
		return Meme{}, ErrInvalidResultURL
	}
	// Jalur presigned PUT tidak bisa memaksa ukuran sebelum upload, jadi objek
	// yang sudah tersimpan diperiksa di sini sebelum meme dianggap selesai.
	for _, key := range []string{s.keyFromURL(meme.BaseImageURL), resultKey(meme.ID)} {
		if err := s.verifyStored(ctx, key); err != nil {
			return Meme{}, err
		}
	}
	return s.repo.SetResultURL(ctx, meme.ID, userID, expected)
}

func resultKey(id string) string {
	return fmt.Sprintf("result/%s.png", id)
}

func (s *Service) List(ctx context.Context, userID string, limit, offset int32) (ListResponse, error) {
	items, total, err := s.repo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return ListResponse{}, err
	}
	if items == nil {
		items = []Meme{}
	}
	return ListResponse{Items: items, Total: total}, nil
}

func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, id, userID)
}

func (s *Service) publicURL(key string) string {
	if s.baseURL == "" {
		return key
	}
	return strings.TrimRight(s.baseURL, "/") + "/" + path.Clean(key)
}

func allowedExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

func allowedContentType(ct string) bool {
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}
