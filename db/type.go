package db

import "time"

type File struct {
	ID               int64
	UserID           int64
	OriginalFilename string
	Filename         string
	Filepath         string
	Filesize         int64
	Filetype         string
	Filehash         string
	Filetag          string
	Filedescription  string
	Processed        bool
	CreatedAt        string
	UpdatedAt        string
}

type User struct {
	ID          int64     `json:"id"`
	ReferenceID string    `json:"reference_id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	Enabled     bool      `json:"enabled"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitzero"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}
