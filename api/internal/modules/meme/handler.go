package meme

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/nyxsr/meme-portal/api/internal/middleware"
	"github.com/nyxsr/meme-portal/api/internal/response"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// prepareUploadDTO adalah body JSON kontrak upload asli.
type prepareUploadDTO struct {
	Title       string `json:"title"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"` // opsional; bila diisi, dibatasi dan ditandatangani ke URL
}

// mediaType mengembalikan media type header Content-Type (kosong bila tidak ada).
func mediaType(r *http.Request) string {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

// PrepareUpload mendukung dua bentuk request:
//   - JSON {title, filename, contentType[, size]} (kontrak asli): membuat meme
//     pending dan membalas uploadUrl (presigned PUT) untuk gambar dasar.
//   - Body gambar mentah (Content-Type image/*): API menyimpan gambar sendiri.
func (h *Handler) PrepareUpload(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "unauthorized")
		return
	}
	if mt := mediaType(r); mt == "" || mt == "application/json" {
		var dto prepareUploadDTO
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&dto); err != nil {
			response.Error(w, http.StatusBadRequest, response.CodeInvalidRequestBody, "invalid request body")
			return
		}
		res, err := h.service.PrepareUploadPresigned(r.Context(), userID, dto.Title, path.Ext(dto.Filename), dto.ContentType, dto.Size)
		if err != nil {
			writeError(w, err)
			return
		}
		response.Success(w, http.StatusCreated, CodeUploadPrepared, res, "upload disiapkan")
		return
	}
	data, ok := readImage(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	res, err := h.service.PrepareUpload(r.Context(), userID, query.Get("title"), path.Ext(query.Get("filename")), data)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Success(w, http.StatusCreated, CodeUploadPrepared, res, "upload disiapkan")
}

type completeDTO struct {
	ResultURL string `json:"resultUrl"`
}

// ResultUpload mendukung dua bentuk request:
//   - tanpa body gambar (kontrak asli): membalas uploadUrl (presigned PUT)
//     untuk gambar hasil render.
//   - body PNG mentah (Content-Type image/png): API menyimpan hasil render sendiri.
func (h *Handler) ResultUpload(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "unauthorized")
		return
	}
	if mediaType(r) != "image/png" {
		res, err := h.service.ResultUploadPresigned(r.Context(), userID, r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		response.Success(w, http.StatusOK, CodeUploadPrepared, res, "upload hasil disiapkan")
		return
	}
	data, ok := readImage(w, r)
	if !ok {
		return
	}
	res, err := h.service.ResultUpload(r.Context(), userID, r.PathValue("id"), data)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Success(w, http.StatusOK, CodeUploadPrepared, res, "upload hasil disiapkan")
}

// Complete: setelah canvas merender & mengunggah hasil ke S3, klien melaporkan URL hasilnya.
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	var dto completeDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil || dto.ResultURL == "" {
		response.Error(w, http.StatusBadRequest, response.CodeInvalidRequestBody, "resultUrl wajib diisi")
		return
	}
	m, err := h.service.Complete(r.Context(), userID, id, dto.ResultURL)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Success(w, http.StatusOK, CodeMemeUpdated, m, "meme selesai")
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "unauthorized")
		return
	}
	limit := clampInt(parseQueryInt(r, "limit", defaultLimit), 1, maxLimit)
	offset := max(parseQueryInt(r, "offset", 0), 0)
	res, err := h.service.List(r.Context(), userID, int32(limit), int32(offset))
	if err != nil {
		writeError(w, err)
		return
	}
	response.Success(w, http.StatusOK, CodeMemeListed, res, "daftar meme")
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "unauthorized")
		return
	}
	if err := h.service.Delete(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	response.Success(w, http.StatusOK, CodeMemeDeleted, nil, "meme dihapus")
}

func readImage(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageBytes))
	if err == nil {
		return data, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeInvalidImage, "ukuran gambar maksimum 10 MiB")
	} else {
		writeError(w, ErrInvalidFile)
	}
	return nil, false
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidResultURL):
		response.Error(w, http.StatusBadRequest, response.CodeInvalidRequestBody, err.Error())
	case errors.Is(err, ErrInvalidFile):
		response.Error(w, http.StatusBadRequest, response.CodeInvalidImage, err.Error())
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeMemeNotFound, err.Error())
	case errors.Is(err, ErrNotSubscribedGate):
		response.Error(w, http.StatusForbidden, response.CodeNotSubscribed, ErrNotSubscribedGate.Error())
	case errors.Is(err, ErrQuotaExceeded):
		response.Error(w, http.StatusForbidden, response.CodeQuotaExceeded, ErrQuotaExceeded.Error())
	default:
		slog.Error("meme handler: internal error", "err", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternalServer, "internal server error")
	}
}

func parseQueryInt(r *http.Request, key string, fallback int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
