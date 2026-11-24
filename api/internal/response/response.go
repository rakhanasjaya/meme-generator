// Package response mendefinisikan envelope sukses/error standar API.
package response

import (
	"encoding/json"
	"net/http"
)

// Kode status aplikasi: prefix per domain (10xxx auth, 20xxx subscription, 30xxx meme).
const (
	CodeOK = 0

	CodeInvalidRequestBody = 10001
	CodeUnauthorized       = 10002
	CodeInvalidCredential  = 10003
	CodeEmailTaken         = 10004

	CodeNotFound          = 20001
	CodeNotSubscribed     = 20002
	CodePaymentFailed     = 20003
	CodePlanNotFound      = 20004
	CodeAlreadySubscribed = 20005
	CodeQuotaExceeded      = 20006

	CodeMemeNotFound   = 30001
	CodeInvalidImage   = 30002
	CodeUploadTooLarge = 30003
	CodeInternalServer = 50000
)

type Body struct {
	Success bool           `json:"success"`
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    any            `json:"data,omitempty"`
	Errors  map[string]any `json:"errors,omitempty"`
}

func Success(w http.ResponseWriter, status int, code int, data any, message string) {
	write(w, status, Body{Success: true, Code: code, Message: message, Data: data})
}

func Error(w http.ResponseWriter, status int, code int, message string) {
	write(w, status, Body{Success: false, Code: code, Message: message})
}

func ErrorWith(w http.ResponseWriter, status int, code int, message string, errs map[string]any) {
	write(w, status, Body{Success: false, Code: code, Message: message, Errors: errs})
}

func write(w http.ResponseWriter, status int, body Body) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
