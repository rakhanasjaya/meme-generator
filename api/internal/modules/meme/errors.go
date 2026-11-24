package meme

import "errors"

var (
	ErrNotFound          = errors.New("meme tidak ditemukan")
	ErrInvalidResultURL  = errors.New("URL hasil meme tidak valid")
	ErrInvalidFile       = errors.New("file gambar tidak valid")
	ErrNotSubscribedGate = errors.New("butuh langganan aktif untuk membuat meme")
	ErrQuotaExceeded     = errors.New("kamu sudah memakai seluruh kuota meme di paket ini")
)
