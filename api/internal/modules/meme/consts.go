package meme

import "time"

const maxImageBytes = 10 << 20

// maxPresignTTL adalah masa berlaku presigned PUT URL.
const maxPresignTTL = 15 * time.Minute

const (
	defaultLimit = 20
	maxLimit     = 100
)
