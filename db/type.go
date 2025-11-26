package db

import "time"

type File struct {
	ID               int64
	UserID           int64
	OriginalFilename string
	Filename         string
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

type Forum struct {
	ID            int64
	ExternalID    string
	TenantID      int64
	WorkspaceID   int64
	OwnerUserID   int64
	OwnerUserName string // Username of the forum creator
	Title         string
	Description   string
	ImageURL      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Thread struct {
	ID            int64
	ExternalID    string
	ForumID       int64
	OwnerUserID   int64
	OwnerUserName string // Username of the thread creator
	Title         string
	ImageURL      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Post struct {
	ID           int64
	ExternalID   string
	ThreadID     int64
	OwnerUserID  int64
	Content      string
	ContentHTML  string
	ParentPostID *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
