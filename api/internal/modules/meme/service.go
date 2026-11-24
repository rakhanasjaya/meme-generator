package meme

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

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
	if len(data) == 0 || len(data) > maxImageBytes || !allowedExt(ext) || mime.TypeByExtension(ext) != contentType {
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
