package provenance

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// Person is the presentable identity of a user id (resolved by the host, the
// kernel does not know the users table).
type Person struct {
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

// PeopleResolver resolves user ids to people (optional).
type PeopleResolver func(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) map[uuid.UUID]Person

// People collects the person ids an entry list mentions.
func People(entries ...Entry) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(id *uuid.UUID) {
		if id != nil && *id != uuid.Nil && !seen[*id] {
			seen[*id] = true
			out = append(out, *id)
		}
	}
	for _, e := range entries {
		if e.ActorKind == string(ActorHuman) {
			add(e.ActorID)
		}
		add(e.OnBehalfOf)
		add(e.AuthorizedBy)
	}
	return out
}

// Handler serves the read API:
//
//	GET <prefix>/:model/:id?limit=200  → {created, last_change, history, people}
//
// The host mounts it behind its own auth and passes how to read the caller's
// organization and whether the caller may read that model.
type Handler struct {
	Store *Store
	// OrgID returns the caller's organization (required).
	OrgID func(c fiber.Ctx) uuid.UUID
	// CanRead gates the model (optional; nil = allowed).
	CanRead func(c fiber.Ctx, model string) bool
	// People resolves names/avatars (optional).
	People PeopleResolver
}

// Mount registers the route on r (e.g. app.Group("/api/provenance")).
func (h *Handler) Mount(r fiber.Router) {
	r.Get("/:model/:id", h.Get)
}

// Get answers one record's provenance.
func (h *Handler) Get(c fiber.Ctx) error {
	if h == nil || h.Store == nil || h.OrgID == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"success": false, "message": "provenance unavailable"})
	}
	org := h.OrgID(c)
	if org == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"success": false, "message": "unauthorized"})
	}
	model, id := c.Params("model"), c.Params("id")
	if model == "" || id == "" || len(id) > 64 || len(model) > 120 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"success": false, "message": "model and id are required"})
	}
	if h.CanRead != nil && !h.CanRead(c, model) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"success": false, "message": "forbidden"})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	v, err := h.Store.ForRecord(c.Context(), org, model, id, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"success": false, "message": "could not read provenance"})
	}
	people := map[string]Person{}
	if h.People != nil {
		for id, p := range h.People(c.Context(), org, People(v.History...)) {
			people[id.String()] = p
		}
	}
	return c.JSON(fiber.Map{"success": true, "data": fiber.Map{
		"created": v.Created, "last_change": v.LastChange, "history": v.History, "people": people,
	}})
}

// Presentation is the wire object a host attaches to a record (e.g. at
// created_by.provenance) so tables can render it without a second request:
// the provenance fields, when it happened and the people's names.
func (e Entry) Presentation(people map[uuid.UUID]Person) map[string]any {
	m := map[string]any{
		"actor_kind": e.ActorKind,
		"mode":       e.Mode,
		"at":         e.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	str := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	str("actor_name", e.ActorName)
	str("via", e.Via)
	str("origin", e.Origin)
	str("ref_kind", e.RefKind)
	str("ref_id", e.RefID)
	str("summary", e.Summary)
	if e.Proposed {
		m["proposed"] = true
	}
	person := func(key, prefix string, id *uuid.UUID) {
		if id == nil || *id == uuid.Nil {
			return
		}
		m[key] = id.String()
		if p, ok := people[*id]; ok {
			n := p.Name
			if n == "" {
				n = p.Email
			}
			str(prefix+"_name", n)
			str(prefix+"_avatar", p.Avatar)
		}
	}
	if e.ActorKind == string(ActorHuman) {
		person("actor_id", "actor_person", e.ActorID)
	} else if e.ActorID != nil {
		m["actor_id"] = e.ActorID.String()
	}
	person("on_behalf_of", "on_behalf_of", e.OnBehalfOf)
	person("authorized_by", "authorized_by", e.AuthorizedBy)
	if e.AuthorizedAt != nil {
		m["authorized_at"] = e.AuthorizedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if e.SessionID != nil {
		m["session_id"] = e.SessionID.String()
	}
	return m
}
