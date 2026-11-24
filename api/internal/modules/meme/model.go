package meme

import "time"

// Meme adalah meme yang sudah dirender (hasil canvas di browser).
type Meme struct {
	ID           string    `json:"id"`
	UserID       string    `json:"userId"`
	Title        string    `json:"title"`
	BaseImageURL string    `json:"baseUrl"`
	ResultURL    *string   `json:"resultUrl,omitempty"`
	TopText      string    `json:"topText"`
	BottomText   string    `json:"bottomText"`
	FontSize     int32     `json:"fontSize"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type UploadResponse struct {
	ID        string `json:"id"`
	UploadURL string `json:"uploadUrl"`
	BaseURL   string `json:"baseUrl"`
}

type ListResponse struct {
	Items []Meme `json:"items"`
	Total int64  `json:"total"`
}
