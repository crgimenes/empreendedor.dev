package user

import "time"

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
