// model.go
package http

import (
	"time"

	"github.com/luisfelipecoelho/go-template/internal/item"
)

// createItemRequest is the POST /v1/items body. Pointer-free: all fields
// required by the spec.
type createItemRequest struct {
	Name       string `json:"name"`
	Quantity   int    `json:"quantity"`
	PriceCents int64  `json:"price_cents"`
}

// updateItemRequest is the PATCH /v1/items/{id} body. All fields optional; at
// least one must be present (enforced by the domain).
type updateItemRequest struct {
	Name       *string `json:"name"`
	Quantity   *int    `json:"quantity"`
	PriceCents *int64  `json:"price_cents"`
}

// itemResponse is the wire representation of an item.
type itemResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Quantity   int       `json:"quantity"`
	PriceCents int64     `json:"price_cents"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// itemListResponse is the paginated list envelope.
type itemListResponse struct {
	Items []itemResponse `json:"items"`
	Page  int            `json:"page"`
	Rows  int            `json:"rows"`
	Total int            `json:"total"`
}

func toItemResponse(itm item.Item) itemResponse {
	return itemResponse{
		ID:         itm.ID,
		Name:       itm.Name,
		Quantity:   itm.Quantity,
		PriceCents: itm.PriceCents,
		CreatedAt:  itm.CreatedAt,
		UpdatedAt:  itm.UpdatedAt,
	}
}
