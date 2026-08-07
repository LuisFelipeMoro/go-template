// Package http is the item domain's HTTP adapter: it maps requests to the
// domain service and domain errors to responses, and registers its routes on
// the kernel's /v1 group via web.RouteRegister. It imports the domain, never
// the reverse, so the domain package stays free of gin and HTTP terms.
package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

// Handler serves the /v1/items operations against the domain service.
type Handler struct {
	svc *item.Service
}

var _ web.RouteRegister = (*Handler)(nil)

// NewHandler constructs the item HTTP handler.
func NewHandler(svc *item.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the item routes on the versioned API group.
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.POST("/items", h.create)
	rg.GET("/items", h.list)
	rg.GET("/items/:id", h.get)
	rg.PATCH("/items/:id", h.update)
	rg.DELETE("/items/:id", h.delete)
}

// create implements operationId createItem.
func (h *Handler) create(c *gin.Context) {
	req, ok := web.BindJSON[createItemRequest](c)
	if !ok {
		return
	}

	itm, err := h.svc.Create(c.Request.Context(), item.NewItem{
		Name:       req.Name,
		Quantity:   req.Quantity,
		PriceCents: req.PriceCents,
	})
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toItemResponse(itm))
}

// list implements operationId listItems.
func (h *Handler) list(c *gin.Context) {
	page, rows, ok := parsePage(c)
	if !ok {
		return
	}

	items, total, err := h.svc.Query(c.Request.Context(), item.Page{Number: page, Rows: rows})
	if err != nil {
		mapDomainError(c, err)
		return
	}

	out := itemListResponse{Items: make([]itemResponse, 0, len(items)), Page: page, Rows: rows, Total: total}
	for _, itm := range items {
		out.Items = append(out.Items, toItemResponse(itm))
	}
	c.JSON(http.StatusOK, out)
}

// get implements operationId getItem.
func (h *Handler) get(c *gin.Context) {
	itm, err := h.svc.QueryByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, toItemResponse(itm))
}

// update implements operationId updateItem.
func (h *Handler) update(c *gin.Context) {
	req, ok := web.BindJSON[updateItemRequest](c)
	if !ok {
		return
	}

	itm, err := h.svc.Update(c.Request.Context(), c.Param("id"), item.UpdateItem{
		Name:       req.Name,
		Quantity:   req.Quantity,
		PriceCents: req.PriceCents,
	})
	if err != nil {
		mapDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, toItemResponse(itm))
}

// delete implements operationId deleteItem.
func (h *Handler) delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		mapDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
