//go:generate go run ../cmd/partial
package test

import (
	"time"

	"gopkg.in/guregu/null.v3"
)

// codegen-partial:builder,matcher
type Organisation struct {
	ID             string      `json:"id" gorm:"type:text;primaryKey;default:generate_ulid()"`
	Name           string      `json:"name"`
	OptionalString null.String `json:"optional_string"`
	BoolFlag       bool        `json:"bool_flag"`
}

// codegen-partial:builder,matcher
type Incident struct {
	ID             string `json:"id" gorm:"type:text;primaryKey;default:generate_ulid()"`
	OrganisationID string `json:"organisation_id"`
	Organisation   *Organisation
	CreatedAt      time.Time `json:"created_at"`
}
